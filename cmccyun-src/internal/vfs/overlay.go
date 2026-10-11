package vfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

// This file implements the CD2-style write model: editing an existing cloud
// object never downloads it in full. The local staging file only holds the
// byte ranges the application actually wrote, and everything else is streamed
// from the cloud on demand — both when reading and when the new object is
// assembled for upload.
//
//	extents: [ ...... written ][ untouched ][ written ][  zeros  ]
//	                 |                |           |         |
//	              staging           cloud      staging    staging(zero)

// extent is one range of the logical file whose authoritative bytes live
// locally. zero=true means the range reads back as zeroes (a grown tail).
type extent struct {
	off    int64
	length int64
	zero   bool
}

func (e extent) end() int64 { return e.off + e.length }

// extents is a sorted, non-overlapping, merged list of local ranges.
type extents []extent

// add records a written range. The new range wins wherever it overlaps, and
// neighbours with the same backing are merged.
func (e extents) add(x extent) extents {
	if x.length <= 0 {
		return e
	}
	all := make(extents, 0, len(e)+1)
	all = append(all, e...)
	all = append(all, x) // the new range sorts last among equal offsets
	sort.SliceStable(all, func(i, j int) bool { return all[i].off < all[j].off })

	out := make(extents, 0, len(all))
	for _, cur := range all {
		if cur.length <= 0 {
			continue
		}
		if len(out) == 0 {
			out = append(out, cur)
			continue
		}
		last := &out[len(out)-1]
		if cur.off >= last.end() {
			if cur.off == last.end() && cur.zero == last.zero {
				last.length = cur.end() - last.off
			} else {
				out = append(out, cur)
			}
			continue
		}
		// cur overlaps last.
		if cur.zero == last.zero {
			if cur.end() > last.end() {
				last.length = cur.end() - last.off
			}
			continue
		}
		// Different backing: the newer range wins in the overlap.
		switch {
		case cur.off == last.off && cur.end() >= last.end():
			*last = cur
		case cur.off == last.off:
			tail := extent{off: cur.end(), length: last.end() - cur.end(), zero: last.zero}
			*last = cur
			out = append(out, tail)
		default:
			end, zero := last.end(), last.zero
			last.length = cur.off - last.off
			out = append(out, cur)
			if cur.end() < end {
				out = append(out, extent{off: cur.end(), length: end - cur.end(), zero: zero})
			}
		}
	}
	return out
}

// find returns the extent containing off, if any.
func (e extents) find(off int64) (extent, bool) {
	lo, hi := 0, len(e)
	for lo < hi {
		mid := (lo + hi) / 2
		if e[mid].end() <= off {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(e) && e[lo].off <= off && off < e[lo].end() {
		return e[lo], true
	}
	return extent{}, false
}

// nextStart returns the offset where the next local range begins, or size when
// there is none — i.e. how far the cloud-backed run from off extends.
func (e extents) nextStart(off, size int64) int64 {
	lo, hi := 0, len(e)
	for lo < hi {
		mid := (lo + hi) / 2
		if e[mid].end() <= off {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(e) && e[lo].off > off {
		return e[lo].off
	}
	return size
}

// trim drops local ranges past size (a shrink) and clamps the rest.
func (e extents) trim(size int64) extents {
	out := make(extents, 0, len(e))
	for _, cur := range e {
		if cur.off >= size {
			continue
		}
		if cur.end() > size {
			cur.length = size - cur.off
		}
		out = append(out, cur)
	}
	return out
}

// overlay presents the logical content of a file whose untouched bytes still
// live in the cloud. It implements io.ReaderAt so it can feed both the FUSE
// read path and the upload path (hash pass + part uploads).
type overlay struct {
	// node, when set, is the live view: size and local ranges are re-read on
	// every call so writes performed through an open handle are visible
	// immediately. The upload path leaves it nil and uses the snapshot below.
	node *Node

	size     int64
	baseSize int64 // size of the cloud object the local edits are based on
	staged   *os.File
	ext      extents
	remote   *remoteStream
	ctx      context.Context
}

var _ io.ReaderAt = (*overlay)(nil)

// state returns the logical size and the local ranges to use for this call.
func (o *overlay) state() (int64, extents) {
	if o.node == nil {
		return o.size, o.ext
	}
	o.node.mu.Lock()
	defer o.node.mu.Unlock()
	return o.node.size, append(extents(nil), o.node.extents...)
}

// ReadAt fills p with the composed content of the file (io.ReaderAt, used by
// the upload path where the context outlives a single FUSE request).
func (o *overlay) ReadAt(p []byte, off int64) (int, error) {
	return o.readAt(o.ctx, p, off)
}

// readAt fills p with the composed content of the file.
func (o *overlay) readAt(ctx context.Context, p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("overlay: negative offset")
	}
	if o.staged == nil && o.remote == nil {
		return 0, errors.New("overlay: no backing")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	size, ext := o.state()

	total := 0
	for total < len(p) {
		cur := off + int64(total)
		if cur >= size {
			break
		}
		want := int64(len(p) - total)
		if rem := size - cur; want > rem {
			want = rem
		}

		if e, ok := ext.find(cur); ok {
			n := e.end() - cur
			if n > want {
				n = want
			}
			if e.zero {
				for i := int64(0); i < n; i++ {
					p[total+int(i)] = 0
				}
			} else {
				if o.staged == nil {
					return total, fmt.Errorf("overlay: 缺少暂存数据 (off=%d)", cur)
				}
				got, err := o.staged.ReadAt(p[total:total+int(n)], cur)
				if err != nil && err != io.EOF {
					return total + got, err
				}
				if int64(got) != n {
					return total + got, io.ErrUnexpectedEOF
				}
			}
			total += int(n)
			continue
		}

		// Cloud-backed run: from cur up to the next local range.
		n := ext.nextStart(cur, size) - cur
		if n > want {
			n = want
		}
		if cur >= o.baseSize || o.remote == nil {
			// Past the end of the cloud object: reads as zeroes.
			for i := int64(0); i < n; i++ {
				p[total+int(i)] = 0
			}
			total += int(n)
			continue
		}
		if lim := o.baseSize - cur; lim < n {
			n = lim
		}
		got, err := o.remote.readAt(ctx, p[total:total+int(n)], cur)
		total += got
		if err != nil {
			if err == io.EOF {
				return total, io.EOF
			}
			return total, err
		}
		if got == 0 {
			return total, io.ErrUnexpectedEOF
		}
	}
	if total < len(p) {
		return total, io.EOF
	}
	return total, nil
}
