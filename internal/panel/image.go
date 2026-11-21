package panel

import (
	"errors"
	"fmt"
	"strings"
)

// CheckImage checks a picture the catcher plays before a card (a gif spot,
// a jingle): a file name in its visual_aid/ (no path) or an https:// URL,
// without a comma (it travels in the comma-separated tags).
func CheckImage(image string) error {
	switch {
	case image == "":
		return errors.New("needs an image: a file in the catcher's visual_aid/ or an https:// URL")
	case strings.ContainsAny(image, ", \t\n"):
		return fmt.Errorf("image %q has a comma or a space", image)
	case strings.HasPrefix(image, "https://"):
		return nil
	case strings.Contains(image, "://"):
		return fmt.Errorf("image %q: only https:// URLs", image)
	case strings.ContainsAny(image, "/\\") || strings.HasPrefix(image, "."):
		return fmt.Errorf("image %q: a file name in visual_aid/, not a path", image)
	}
	return nil
}
