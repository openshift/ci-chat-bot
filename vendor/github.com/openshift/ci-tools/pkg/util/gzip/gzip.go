package gzip

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"
	"os"
)

// ReadFileMaybeGZIP wraps util.ReadBytesMaybeGZIP, returning the decompressed contents
// if the file is gzipped, or otherwise the raw contents
func ReadFileMaybeGZIP(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ReadBytesMaybeGZIP(b)
}

func ReadBytesMaybeGZIP(data []byte) ([]byte, error) {
	// check if data contains gzip header: http://www.zlib.org/rfc-gzip.html
	if !bytes.HasPrefix(data, []byte("\x1F\x8B")) {
		// go ahead and return the contents if not gzipped
		return data, nil
	}
	// otherwise decode
	gzipReader, err := gzip.NewReader(bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(gzipReader)
}

func CompressStringAndBase64(data string) (string, error) {
	buf := new(bytes.Buffer)
	writer, err := gzip.NewWriterLevel(buf, gzip.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err := writer.Write([]byte(data)); err != nil {
		return "", err
	}
	writer.Close()
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
