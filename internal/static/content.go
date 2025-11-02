package static

import "embed"

//go:embed *.html *.sh
var StaticFiles embed.FS

// FaviconDataURL generates a simple SVG favicon data URL
func FaviconDataURL() string {
	svgContent := `<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 32 32"><circle cx="16" cy="16" r="16" fill="#f3f4f6"/><text x="16" y="22" font-family="Arial, sans-serif" font-size="20" font-weight="bold" fill="#4285f4" text-anchor="middle">Y</text></svg>`
	return "data:image/svg+xml;base64," + base64Encode(svgContent)
}

// base64Encode encodes a string to base64
func base64Encode(s string) string {
	const base64Chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

	input := []byte(s)
	var result []byte

	for i := 0; i < len(input); i += 3 {
		var b1, b2, b3 byte
		b1 = input[i]

		if i+1 < len(input) {
			b2 = input[i+1]
		}
		if i+2 < len(input) {
			b3 = input[i+2]
		}

		// Convert 3 bytes to 4 base64 characters
		result = append(result, base64Chars[b1>>2])
		result = append(result, base64Chars[((b1&0x03)<<4)|((b2&0xf0)>>4)])

		if i+1 < len(input) {
			result = append(result, base64Chars[((b2&0x0f)<<2)|((b3&0xc0)>>6)])
		} else {
			result = append(result, '=')
		}

		if i+2 < len(input) {
			result = append(result, base64Chars[b3&0x3f])
		} else {
			result = append(result, '=')
		}
	}

	return string(result)
}
