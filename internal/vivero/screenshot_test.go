package vivero

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCropScreenshotOuterWhitespaceRemovesBlankViewportPadding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shot.png")
	img := image.NewRGBA(image.Rect(0, 0, 1280, 884))
	bg := color.RGBA{R: 248, G: 248, B: 244, A: 255}
	draw.Draw(img, img.Bounds(), &image.Uniform{C: bg}, image.Point{}, draw.Src)

	// Model an app-shell screenshot: real UI in the top-left, blank app
	// background filling the rest of the Playwright viewport.
	draw.Draw(img, image.Rect(0, 0, 640, 442), &image.Uniform{C: color.Black}, image.Point{}, draw.Src)

	writePNG(t, path, img)
	result, err := cropScreenshotOuterWhitespace(path)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Cropped {
		t.Fatal("expected screenshot to be cropped")
	}
	if result.OriginalWidth != 1280 || result.OriginalHeight != 884 {
		t.Fatalf("original dimensions = %dx%d", result.OriginalWidth, result.OriginalHeight)
	}
	if result.Width > 700 || result.Height > 500 {
		t.Fatalf("cropped dimensions still include too much blank space: %dx%d", result.Width, result.Height)
	}

	cropped := readPNG(t, path)
	if cropped.Bounds().Dx() != result.Width || cropped.Bounds().Dy() != result.Height {
		t.Fatalf("written dimensions = %dx%d, result = %dx%d", cropped.Bounds().Dx(), cropped.Bounds().Dy(), result.Width, result.Height)
	}
}

func TestCropScreenshotOuterWhitespaceLeavesUniformScreenshotsAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shot.png")
	img := image.NewRGBA(image.Rect(0, 0, 640, 480))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.Black}, image.Point{}, draw.Src)

	writePNG(t, path, img)
	result, err := cropScreenshotOuterWhitespace(path)
	if err != nil {
		t.Fatal(err)
	}
	if result.Cropped {
		t.Fatalf("uniform screenshot should not be cropped: %#v", result)
	}
	if result.Width != 640 || result.Height != 480 {
		t.Fatalf("dimensions changed: %#v", result)
	}
}

func TestNormalizeScreenshotOptionsDefaultsToViewportCapture(t *testing.T) {
	opts := normalizeScreenshotOptions(ScreenshotOptions{})
	if opts.Path != "/" {
		t.Fatalf("path = %q", opts.Path)
	}
	if opts.Width != 1280 || opts.Height != 800 {
		t.Fatalf("viewport = %dx%d", opts.Width, opts.Height)
	}
	if opts.Crop {
		t.Fatal("crop should be opt-in so screenshots stay true viewport-sized by default")
	}
}

func TestScreenshotOutputPathKeepsPreviewUnderScreenshotsDir(t *testing.T) {
	home := t.TempDir()
	out := screenshotOutputPath(home, "", "../outside", "../web", "/", ScreenshotBreakpoint{}, false, "")
	root := filepath.Join(home, "screenshots")
	if !pathWithinRoot(root, out) {
		t.Fatalf("screenshot path escaped root: out=%s root=%s", out, root)
	}
	if filepath.Dir(out) == filepath.Join(home, "outside") || strings.Contains(out, "..") {
		t.Fatalf("screenshot path preserved traversal elements: %s", out)
	}
}

func TestScreenshotOutputPathKeepsServiceUnderCustomOutputDir(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "custom-shots")
	out := screenshotOutputPath(home, root, "preview", "../../web", "/", ScreenshotBreakpoint{}, false, "")
	if !pathWithinRoot(root, out) {
		t.Fatalf("screenshot path escaped custom output dir: out=%s root=%s", out, root)
	}
}

func TestParseScreenshotBreakpoint(t *testing.T) {
	bp, err := parseScreenshotBreakpoint("mobile=390x844")
	if err != nil {
		t.Fatal(err)
	}
	if bp.Name != "mobile" || bp.Width != 390 || bp.Height != 844 {
		t.Fatalf("breakpoint = %#v", bp)
	}

	bp, err = parseScreenshotBreakpoint("1280x720")
	if err != nil {
		t.Fatal(err)
	}
	if bp.Name != "1280x720" || bp.Width != 1280 || bp.Height != 720 {
		t.Fatalf("unnamed breakpoint = %#v", bp)
	}
}

func TestScreenshotBreakpointsPreferExplicitThenProjectThenViewport(t *testing.T) {
	explicit := []ScreenshotBreakpoint{{Name: "desktop", Width: 1440, Height: 900}}
	project := []ScreenshotBreakpoint{{Name: "mobile", Width: 390, Height: 844}}
	got := screenshotBreakpoints(ScreenshotOptions{Breakpoints: explicit, UseProjectBreakpoints: true}, project)
	if len(got) != 1 || got[0].Name != "desktop" {
		t.Fatalf("explicit breakpoints not preferred: %#v", got)
	}
	got = screenshotBreakpoints(ScreenshotOptions{UseProjectBreakpoints: true}, project)
	if len(got) != 1 || got[0].Name != "mobile" {
		t.Fatalf("project breakpoints not used: %#v", got)
	}
	got = screenshotBreakpoints(ScreenshotOptions{Width: 800, Height: 600}, project)
	if len(got) != 1 || got[0].Width != 800 || got[0].Height != 600 {
		t.Fatalf("viewport fallback = %#v", got)
	}
}

func TestScreenshotDimensionsAndNameHelpers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shot.png")
	img := image.NewRGBA(image.Rect(0, 0, 37, 23))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	writePNG(t, path, img)

	width, height, err := screenshotDimensions(path)
	if err != nil {
		t.Fatal(err)
	}
	if width != 37 || height != 23 {
		t.Fatalf("screenshotDimensions = %dx%d", width, height)
	}
	if _, _, err := screenshotDimensions(filepath.Join(t.TempDir(), "missing.png")); err == nil {
		t.Fatal("screenshotDimensions should fail for missing files")
	}
	if got := sanitizeScreenshotName(" !!! "); got != "viewport" {
		t.Fatalf("sanitizeScreenshotName empty fallback = %q", got)
	}
	if got := screenshotFileBase("web", "/account?tab=billing", ScreenshotBreakpoint{Width: 390, Height: 844}, true, "Dark Mode"); got != "web-_account_tab_billing-390x844-dark-mode.png" {
		t.Fatalf("screenshotFileBase = %q", got)
	}
}

func TestScreenshotValidationRejectsBadOptions(t *testing.T) {
	for _, spec := range []string{"desktop", "desktop=0x800", "desktop=800x0"} {
		if _, err := parseScreenshotBreakpoint(spec); err == nil {
			t.Fatalf("parseScreenshotBreakpoint(%q) should fail", spec)
		}
	}
	if err := validateColorSchemes([]string{"light", "sepia"}); err == nil || !strings.Contains(err.Error(), "light or dark") {
		t.Fatalf("expected color scheme validation error, got %v", err)
	}
	if got := normalizeArtifactTarget("external"); got != "public" {
		t.Fatalf("normalizeArtifactTarget external = %q", got)
	}
	if got := normalizeArtifactTarget("direct"); got != "origin" {
		t.Fatalf("normalizeArtifactTarget direct = %q", got)
	}
	if got := normalizeArtifactTarget("staging"); got != "staging" {
		t.Fatalf("normalizeArtifactTarget custom = %q", got)
	}
}

func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func readPNG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(f)
	closeErr := f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	return img
}
