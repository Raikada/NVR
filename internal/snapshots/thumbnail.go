package snapshots

import (
	"bytes"
	"image"
	"image/jpeg"
)

// jpegDims decodes just the JPEG header for dimensions.
func jpegDims(data []byte) (int, int, error) {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}

// makeThumbnail downscales to targetWidth (aspect preserved) with
// nearest-neighbor sampling — stdlib only, plenty for list thumbnails.
// Images at or under targetWidth re-encode as-is.
func makeThumbnail(data []byte, targetWidth int) ([]byte, int, int, error) {
	src, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, err
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	tw := targetWidth
	if w <= targetWidth {
		tw = w
	}
	th := h * tw / w
	if th < 1 {
		th = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	for y := 0; y < th; y++ {
		sy := b.Min.Y + y*h/th
		for x := 0; x < tw; x++ {
			sx := b.Min.X + x*w/tw
			dst.Set(x, y, src.At(sx, sy))
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 70}); err != nil {
		return nil, 0, 0, err
	}
	return buf.Bytes(), tw, th, nil
}
