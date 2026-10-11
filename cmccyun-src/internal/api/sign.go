package api

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"sort"
	"strings"
)

// encodeURIComponent mirrors JavaScript's encodeURIComponent as expected by the
// Mcloud-Sign algorithm: query escaping with the "safe" characters that JS
// keeps literal and '+' rendered as %20.
func encodeURIComponent(s string) string {
	r := url.QueryEscape(s)
	r = strings.ReplaceAll(r, "+", "%20")
	r = strings.ReplaceAll(r, "%21", "!")
	r = strings.ReplaceAll(r, "%27", "'")
	r = strings.ReplaceAll(r, "%28", "(")
	r = strings.ReplaceAll(r, "%29", ")")
	r = strings.ReplaceAll(r, "%2A", "*")
	return r
}

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// calSign computes the value used in the `Mcloud-Sign` request header.
//
//	"<timestamp>,<random>,<calSign(body, timestamp, random)>"
//
// The algorithm is:
//  1. URL-encode the raw JSON body the same way the web client does.
//  2. Split it into single characters and sort them.
//  3. Base64-encode the sorted string.
//  4. md5(base64) + md5(ts + ":" + rand) -> md5 -> upper case.
func calSign(body, ts, randStr string) string {
	body = encodeURIComponent(body)
	chars := strings.Split(body, "")
	sort.Strings(chars)
	body = strings.Join(chars, "")
	body = base64.StdEncoding.EncodeToString([]byte(body))
	res := md5Hex(body) + md5Hex(ts+":"+randStr)
	return strings.ToUpper(md5Hex(res))
}
