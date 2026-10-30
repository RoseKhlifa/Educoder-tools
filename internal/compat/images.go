package compat

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"os"
	"strings"
)

func restrictedImageHost(host string) bool {
	return host == "data.educoder.net" || host == "public.educoder.net" || host == "prod-attachments.oss-accelerate.aliyuncs.com"
}

func allowedImagePath(host, path string) bool {
	if host == "prod-attachments.oss-accelerate.aliyuncs.com" {
		// Two public homepage card illustrations observed in the resource set.
		return path == "2024/06/3e612f197b3294f809afa34c748d83a8_1719380870470.png" || path == "2024/06/5a40e47498ad5f1e355ecd4395085f2f_1719380881484.png"
	}
	if strings.HasPrefix(path, "images/") {
		return true
	}
	return host == "public.educoder.net" && (strings.HasPrefix(path, "PortalImage/") || strings.HasPrefix(path, "HomeDiscipline/"))
}

// localImageType reads actual local bytes because upstream avatars can have no
// filename extension and may have been delivered as text/plain. Never serve an
// HTML error response as an image. SVG needs explicit detection: Go's built-in
// sniffer classifies the XML declaration as text/xml instead of image/svg+xml.
func localImageType(path string) (string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()
	buffer := make([]byte, 4096)
	n, err := io.ReadFull(file, buffer)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", false
	}
	buffer = buffer[:n]
	kind := http.DetectContentType(buffer)
	if strings.HasPrefix(kind, "image/") {
		return kind, true
	}
	decoder := xml.NewDecoder(bytes.NewReader(buffer))
	for {
		token, err := decoder.Token()
		if err != nil {
			return "", false
		}
		if start, ok := token.(xml.StartElement); ok {
			return "image/svg+xml", start.Name.Local == "svg" && (start.Name.Space == "" || start.Name.Space == "http://www.w3.org/2000/svg")
		}
	}
}
