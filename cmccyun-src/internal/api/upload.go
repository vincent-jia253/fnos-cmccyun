package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"time"
)

const (
	mb = 1 << 20
	gb = 1 << 30
	// maxPartsPerRequest is the documented ceiling for one /file/create or
	// /file/getUploadUrl call.
	maxPartsPerRequest = 100
)

// UploadOptions tunes the upload path.
type UploadOptions struct {
	// PartSize in bytes. Zero selects 100 MiB (512 MiB for files > 30 GiB),
	// matching the first-party client. Keep it below 5 GiB: the backing EOS
	// object store rejects single objects larger than that.
	PartSize int64
	// OnProgress, when set, is called with the number of bytes transferred.
	OnProgress func(int64)
}

func (o UploadOptions) partSize(size int64) int64 {
	if o.PartSize > 0 {
		return o.PartSize
	}
	if size/gb > 30 {
		return 512 * mb
	}
	return 100 * mb
}

// UploadFile pushes a local file into the cloud drive and returns the name the
// service actually stored it under (it may rename on conflict).
func (c *Client) UploadFile(ctx context.Context, parentID, name, path string, opt UploadOptions) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	return c.UploadReader(ctx, parentID, name, f, st.Size(), opt)
}

// UploadReader pushes size bytes read from src into the cloud drive. src may be
// anything that supports random access, so the caller can assemble the content
// on the fly (local edits stitched onto untouched cloud ranges) without ever
// writing the whole file to disk.
func (c *Client) UploadReader(ctx context.Context, parentID, name string, src io.ReaderAt, size int64, opt UploadOptions) (string, error) {
	if src == nil {
		return "", fmt.Errorf("上传源为空")
	}
	if size < 0 {
		return "", fmt.Errorf("上传大小无效: %d", size)
	}

	// The service needs a whole-file SHA-256 before the upload is created.
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(src, 0, size)); err != nil {
		return "", err
	}
	fullHash := hex.EncodeToString(h.Sum(nil))

	partSize := opt.partSize(size)
	partCount := size / partSize
	if size%partSize > 0 {
		partCount++
	}
	if partCount == 0 {
		partCount = 1
	}

	reqParts := make([]partInfo, 0, partCount)
	for i := int64(0); i < partCount; i++ {
		start := i * partSize
		byteSize := size - start
		if byteSize > partSize {
			byteSize = partSize
		}
		reqParts = append(reqParts, partInfo{PartNumber: int(i) + 1, PartSize: byteSize})
	}

	first := reqParts
	if len(first) > maxPartsPerRequest {
		first = first[:maxPartsPerRequest]
	}

	create := map[string]any{
		"contentHash":          fullHash,
		"contentHashAlgorithm": "SHA256",
		"contentType":          "application/octet-stream",
		"parallelUpload":       false,
		"partInfos":            first,
		"size":                 size,
		"parentFileId":         parentID,
		"name":                 name,
		"type":                 "file",
		"fileRenameMode":       "auto_rename",
	}
	var resp createResp
	if err := c.post(ctx, "/file/create", create, &resp); err != nil {
		return "", err
	}
	stored := resp.Data.FileName
	if stored == "" {
		stored = name
	}
	// The service already holds an identical file: nothing to transfer.
	if resp.Data.Exist {
		return stored, nil
	}
	if len(resp.Data.PartInfos) == 0 {
		// Instant upload (hash hit) or a zero-byte file.
		if size == 0 {
			if err := retryDo(ctx, 3, func() error {
				_, cerr := c.personalComplete(ctx, resp.Data.FileId, resp.Data.UploadId, fullHash)
				return cerr
			}); err != nil {
				return "", err
			}
		}
		return stored, nil
	}

	urls := append([]partInfo(nil), resp.Data.PartInfos...)
	if len(urls) < len(reqParts) {
		more, err := c.signParts(ctx, resp.Data.FileId, resp.Data.UploadId, reqParts[len(urls):])
		if err != nil {
			return "", err
		}
		urls = append(urls, more...)
	}
	sort.Slice(urls, func(i, j int) bool { return urls[i].PartNumber < urls[j].PartNumber })

	// The service requires strictly sequential part uploads. A pre-signed part
	// URL can be rejected mid-transfer (its signature expired, or the ticket was
	// rotated meanwhile); re-signing the remaining parts once recovers the
	// upload instead of losing hours of transfer.
	done := 0
	resigned := false
	for i := 0; i < len(urls); i++ {
		p := urls[i]
		if p.PartNumber < 1 || p.PartNumber > len(reqParts) {
			continue
		}
		sz := reqParts[p.PartNumber-1].PartSize
		if err := c.putPart(ctx, p.UploadUrl, src, (int64(p.PartNumber)-1)*partSize, sz); err != nil {
			// A rejected part URL is usually a rotated credential or an expired
			// signature: getting fresh addresses for the remaining parts is far
			// cheaper than losing the whole transfer.
			if !resigned && ctx.Err() == nil && (authFailure(err) || staleURL(err)) {
				c.noteRecovery("第 %d 分片地址被拒（%v），重新签发剩余分片地址后继续", p.PartNumber, err)
				if fresh, serr := c.signParts(ctx, resp.Data.FileId, resp.Data.UploadId, reqParts[i:]); serr == nil && len(fresh) > 0 {
					resigned = true
					urls = append(urls[:i:i], fresh...)
					sort.Slice(urls, func(a, b int) bool { return urls[a].PartNumber < urls[b].PartNumber })
					i--
					continue
				}
			}
			return "", err
		}
		done += int(sz)
		if opt.OnProgress != nil {
			opt.OnProgress(int64(done))
		}
	}

	if err := retryDo(ctx, 3, func() error {
		_, err := c.personalComplete(ctx, resp.Data.FileId, resp.Data.UploadId, fullHash)
		return err
	}); err != nil {
		return "", err
	}
	return stored, nil
}

// signParts asks the service for pre-signed URLs covering the given parts,
// batching them the way /file/getUploadUrl allows.
func (c *Client) signParts(ctx context.Context, fileID, uploadID string, parts []partInfo) ([]partInfo, error) {
	var all []partInfo
	for i := 0; i < len(parts); i += maxPartsPerRequest {
		end := i + maxPartsPerRequest
		if end > len(parts) {
			end = len(parts)
		}
		payload := map[string]any{
			"fileId":    fileID,
			"uploadId":  uploadID,
			"partInfos": parts[i:end],
			"commonAccountInfo": map[string]any{
				"account":     c.Account(),
				"accountType": 1,
			},
		}
		var more uploadURLResp
		if err := c.post(ctx, "/file/getUploadUrl", payload, &more); err != nil {
			return nil, err
		}
		all = append(all, more.Data.PartInfos...)
	}
	return all, nil
}

func (c *Client) personalComplete(ctx context.Context, fileID, uploadID, fullHash string) ([]byte, error) {
	payload := map[string]any{
		"contentHash":          fullHash,
		"contentHashAlgorithm": "SHA256",
		"fileId":               fileID,
		"uploadId":             uploadID,
	}
	return nil, c.post(ctx, "/file/complete", payload, nil)
}

// putPart uploads one part, retrying transient failures with a backoff so a
// single network hiccup does not restart the whole transfer.
func (c *Client) putPart(ctx context.Context, url string, src io.ReaderAt, off, size int64) error {
	if url == "" {
		return fmt.Errorf("第 %d 分片缺少上传地址", off)
	}
	return retryDo(ctx, 3, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, url,
			io.NewSectionReader(src, off, size))
		if err != nil {
			return err
		}
		req.ContentLength = size
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Content-Length", fmt.Sprint(size))
		req.Header.Set("Origin", "https://yun.139.com")
		req.Header.Set("Referer", "https://yun.139.com/")

		client := &http.Client{Timeout: 0}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated &&
			resp.StatusCode != http.StatusNoContent {
			return &APIError{
				Message: fmt.Sprintf("上传分片失败: HTTP %d", resp.StatusCode),
				Status:  resp.StatusCode,
			}
		}
		return nil
	})
}

// WaitSettle gives the service a moment to index freshly written objects,
// which avoids stale directory listings right after a write.
func WaitSettle() { time.Sleep(300 * time.Millisecond) }
