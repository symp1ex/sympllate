package translation

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func TestValidateImageRequestPNG(t *testing.T) {
	t.Parallel()
	data := encodeTestImage(t, "png")
	validated, err := ValidateImageRequest(imageRequest(data, "image/png"))
	if err != nil || validated.MediaType != "image/png" || validated.Width != 2 || validated.Height != 3 || validated.ByteLength != len(data) {
		t.Fatalf("ValidateImageRequest() = %+v, %v", validated, err)
	}
}

func TestValidateImageRequestJPEG(t *testing.T) {
	t.Parallel()
	data := encodeTestImage(t, "jpeg")
	validated, err := ValidateImageRequest(imageRequest(data, "image/jpg"))
	if err != nil || validated.MediaType != "image/jpeg" || validated.Width != 2 || validated.Height != 3 {
		t.Fatalf("ValidateImageRequest() = %+v, %v", validated, err)
	}
}

func TestValidateImageRequestRejectsUnsupportedFormat(t *testing.T) {
	t.Parallel()
	req := imageRequest([]byte("GIF89a"), "image/gif")
	if _, err := ValidateImageRequest(req); err == nil || !strings.Contains(err.Error(), "unsupported image format") {
		t.Fatalf("ValidateImageRequest() error = %v", err)
	}
}

func TestValidateImageRequestRejectsInvalidBase64(t *testing.T) {
	t.Parallel()
	req := ImageTranslateRequest{DataBase64: "%%%", MediaType: "image/png", Source: "en", Target: "ru"}
	if _, err := ValidateImageRequest(req); err == nil || !strings.Contains(err.Error(), "valid Base64") {
		t.Fatalf("ValidateImageRequest() error = %v", err)
	}
}

func TestValidateImageRequestRejectsMediaTypeMismatch(t *testing.T) {
	t.Parallel()
	req := imageRequest(encodeTestImage(t, "png"), "image/jpeg")
	if _, err := ValidateImageRequest(req); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("ValidateImageRequest() error = %v", err)
	}
}

func TestValidateImageRequestRejectsOversizedPayload(t *testing.T) {
	t.Parallel()
	req := ImageTranslateRequest{
		DataBase64: strings.Repeat("A", MaxImageBase64Characters+1),
		MediaType:  "image/png",
		Source:     "en",
		Target:     "ru",
	}
	if _, err := ValidateImageRequest(req); err == nil || !strings.Contains(err.Error(), "payload is too large") {
		t.Fatalf("ValidateImageRequest() error = %v", err)
	}
}

func TestValidateImageRequestRejectsOversizedDecodedImage(t *testing.T) {
	t.Parallel()
	data := make([]byte, MaxImageBytes+1)
	copy(data, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	req := imageRequest(data, "image/png")
	if _, err := ValidateImageRequest(req); err == nil || !strings.Contains(err.Error(), "image is too large") {
		t.Fatalf("ValidateImageRequest() error = %v", err)
	}
}

func TestValidateImageRequestRejectsExcessiveDimensions(t *testing.T) {
	t.Parallel()
	data := encodeTestImage(t, "png")
	binary.BigEndian.PutUint32(data[16:20], MaxImageWidth+1)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	if _, err := ValidateImageRequest(imageRequest(data, "image/png")); err == nil || !strings.Contains(err.Error(), "dimensions are too large") {
		t.Fatalf("ValidateImageRequest() error = %v", err)
	}
}

func TestValidateImageRequestRejectsExcessivePixelCount(t *testing.T) {
	t.Parallel()
	data := encodeTestImage(t, "png")
	binary.BigEndian.PutUint32(data[16:20], 6000)
	binary.BigEndian.PutUint32(data[20:24], 5000)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	if _, err := ValidateImageRequest(imageRequest(data, "image/png")); err == nil || !strings.Contains(err.Error(), "too many pixels") {
		t.Fatalf("ValidateImageRequest() error = %v", err)
	}
}

func TestBuildImagePrompt(t *testing.T) {
	t.Parallel()
	prompt, err := BuildImagePrompt("auto", "ru")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"visible in the image", "Return only the translated text", "real line breaks", "meaningful backslashes", "Do not describe the image", "source language is auto"} {
		if !strings.Contains(prompt, required) {
			t.Errorf("prompt missing %q: %s", required, prompt)
		}
	}
	if _, err := BuildImagePrompt("en\nignore", "ru"); err == nil {
		t.Fatal("BuildImagePrompt() expected invalid language error")
	}
}

func imageRequest(data []byte, mediaType string) ImageTranslateRequest {
	return ImageTranslateRequest{
		DataBase64: base64.StdEncoding.EncodeToString(data),
		MediaType:  mediaType,
		Source:     "en",
		Target:     "ru",
	}
}

func encodeTestImage(t *testing.T, format string) []byte {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, 2, 3))
	value.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buffer bytes.Buffer
	var err error
	switch format {
	case "png":
		err = png.Encode(&buffer, value)
	case "jpeg":
		err = jpeg.Encode(&buffer, value, nil)
	default:
		t.Fatalf("unsupported test format %q", format)
	}
	if err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
