package vivero

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultScreenshotWidth            = 1280
	defaultScreenshotHeight           = 800
	defaultDeviceScaleFactor          = 1
	defaultArtifactTarget             = "local"
	artifactTargetPublic              = "public"
	artifactTargetOrigin              = "origin"
	screenshotCropPadding             = 24
	screenshotBackgroundThreshold     = 18
	screenshotCornerClusterThreshold  = 12
	screenshotMinimumUsefulCropPixels = 16
)

type screenshotCropResult struct {
	Cropped        bool
	OriginalWidth  int
	OriginalHeight int
	Width          int
	Height         int
}

func normalizeScreenshotOptions(opts ScreenshotOptions) ScreenshotOptions {
	if opts.Path == "" {
		opts.Path = "/"
	}
	opts.Target = normalizeArtifactTarget(opts.Target)
	opts.ColorScheme = normalizeColorScheme(opts.ColorScheme)
	if opts.Width == 0 {
		opts.Width = defaultScreenshotWidth
	}
	if opts.Height == 0 {
		opts.Height = defaultScreenshotHeight
	}
	if opts.DeviceScaleFactor == 0 {
		opts.DeviceScaleFactor = defaultDeviceScaleFactor
	}
	return opts
}

func normalizeQARecordOptions(opts QARecordOptions) QARecordOptions {
	opts.ColorScheme = normalizeColorScheme(opts.ColorScheme)
	if opts.Width == 0 {
		opts.Width = defaultScreenshotWidth
	}
	if opts.Height == 0 {
		opts.Height = defaultScreenshotHeight
	}
	if opts.DeviceScaleFactor == 0 {
		opts.DeviceScaleFactor = defaultDeviceScaleFactor
	}
	if opts.Format == "" {
		opts.Format = "mp4"
	} else {
		opts.Format = strings.ToLower(strings.TrimSpace(opts.Format))
	}
	if opts.WaitMS == 0 {
		opts.WaitMS = 350
	}
	return opts
}

func normalizeColorScheme(colorScheme string) string {
	return strings.ToLower(strings.TrimSpace(colorScheme))
}

func normalizeColorSchemes(colorSchemes []string) []string {
	if len(colorSchemes) == 0 {
		return []string{""}
	}
	seen := map[string]bool{}
	out := []string{}
	for _, colorScheme := range colorSchemes {
		normalized := normalizeColorScheme(colorScheme)
		if normalized == "" {
			continue
		}
		if seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, normalized)
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

func validateColorScheme(colorScheme string) error {
	if colorScheme == "" || colorScheme == "light" || colorScheme == "dark" {
		return nil
	}
	return fmt.Errorf("color scheme must be light or dark: %s", colorScheme)
}

func validateColorSchemes(colorSchemes []string) error {
	for _, colorScheme := range normalizeColorSchemes(colorSchemes) {
		if err := validateColorScheme(colorScheme); err != nil {
			return err
		}
	}
	return nil
}

func normalizeArtifactTarget(target string) string {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "", "local":
		return defaultArtifactTarget
	case "public", "external", "tunnel":
		return artifactTargetPublic
	case "origin", "direct":
		return artifactTargetOrigin
	default:
		return strings.ToLower(strings.TrimSpace(target))
	}
}

func parseScreenshotBreakpoint(spec string) (ScreenshotBreakpoint, error) {
	name := ""
	size := spec
	if before, after, ok := strings.Cut(spec, "="); ok {
		name = strings.TrimSpace(before)
		size = strings.TrimSpace(after)
	}
	widthText, heightText, ok := strings.Cut(strings.ToLower(size), "x")
	if !ok {
		return ScreenshotBreakpoint{}, fmt.Errorf("breakpoint must be name=WIDTHxHEIGHT or WIDTHxHEIGHT: %s", spec)
	}
	width, err := strconv.Atoi(strings.TrimSpace(widthText))
	if err != nil || width <= 0 {
		return ScreenshotBreakpoint{}, fmt.Errorf("invalid breakpoint width: %s", spec)
	}
	height, err := strconv.Atoi(strings.TrimSpace(heightText))
	if err != nil || height <= 0 {
		return ScreenshotBreakpoint{}, fmt.Errorf("invalid breakpoint height: %s", spec)
	}
	if name == "" {
		name = fmt.Sprintf("%dx%d", width, height)
	}
	return ScreenshotBreakpoint{Name: name, Width: width, Height: height}, nil
}

func screenshotBreakpoints(opts ScreenshotOptions, project []ScreenshotBreakpoint) []ScreenshotBreakpoint {
	if len(opts.Breakpoints) > 0 {
		return opts.Breakpoints
	}
	if opts.UseProjectBreakpoints && len(project) > 0 {
		return project
	}
	return []ScreenshotBreakpoint{{Width: opts.Width, Height: opts.Height}}
}

func screenshotFileBase(service, pagePath string, bp ScreenshotBreakpoint, multi bool, colorScheme string) string {
	replacer := strings.NewReplacer("/", "_", ":", "_", "?", "_", "&", "_", "=", "_")
	base := service + "-" + replacer.Replace(pagePath)
	if multi || bp.Name != "" {
		name := bp.Name
		if name == "" {
			name = fmt.Sprintf("%dx%d", bp.Width, bp.Height)
		}
		base += "-" + sanitizeScreenshotName(name)
	}
	if colorScheme != "" {
		base += "-" + sanitizeScreenshotName(colorScheme)
	}
	return base + ".png"
}

func screenshotOutputPath(home, outputDir, previewID, service, pagePath string, bp ScreenshotBreakpoint, multi bool, colorScheme string) string {
	if outputDir != "" {
		return filepath.Join(expandPath(outputDir), screenshotFileBase(service, pagePath, bp, multi, colorScheme))
	}
	return filepath.Join(home, "screenshots", previewID, screenshotFileBase(service, pagePath, bp, multi, colorScheme))
}

func sanitizeScreenshotName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		keep := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if keep {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "viewport"
	}
	return out
}

func screenshotDimensions(path string) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	cfg, err := png.DecodeConfig(f)
	closeErr := f.Close()
	if err != nil {
		return 0, 0, err
	}
	if closeErr != nil {
		return 0, 0, closeErr
	}
	return cfg.Width, cfg.Height, nil
}

func cropScreenshotOuterWhitespace(path string) (screenshotCropResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return screenshotCropResult{}, err
	}
	img, err := png.Decode(f)
	closeErr := f.Close()
	if err != nil {
		return screenshotCropResult{}, err
	}
	if closeErr != nil {
		return screenshotCropResult{}, closeErr
	}

	bounds := img.Bounds()
	result := screenshotCropResult{
		OriginalWidth:  bounds.Dx(),
		OriginalHeight: bounds.Dy(),
		Width:          bounds.Dx(),
		Height:         bounds.Dy(),
	}
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return result, nil
	}

	bg := dominantCornerColor(img, bounds)
	minX, minY := bounds.Max.X, bounds.Max.Y
	maxX, maxY := bounds.Min.X-1, bounds.Min.Y-1
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if nearColor(img.At(x, y), bg, screenshotBackgroundThreshold) {
				continue
			}
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x > maxX {
				maxX = x
			}
			if y > maxY {
				maxY = y
			}
		}
	}
	if maxX < minX || maxY < minY {
		return result, nil
	}

	crop := image.Rect(
		maxInt(bounds.Min.X, minX-screenshotCropPadding),
		maxInt(bounds.Min.Y, minY-screenshotCropPadding),
		minInt(bounds.Max.X, maxX+screenshotCropPadding+1),
		minInt(bounds.Max.Y, maxY+screenshotCropPadding+1),
	)
	if crop.Empty() {
		return result, nil
	}
	if crop == bounds || (bounds.Dx()-crop.Dx() < screenshotMinimumUsefulCropPixels && bounds.Dy()-crop.Dy() < screenshotMinimumUsefulCropPixels) {
		return result, nil
	}

	cropped := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
	draw.Draw(cropped, cropped.Bounds(), img, crop.Min, draw.Src)

	tmp := path + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return result, err
	}
	if err := png.Encode(out, cropped); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return result, err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return result, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return result, err
	}

	result.Cropped = true
	result.Width = crop.Dx()
	result.Height = crop.Dy()
	return result, nil
}

func dominantCornerColor(img image.Image, bounds image.Rectangle) color.RGBA {
	patch := 5
	samples := []color.RGBA{
		averagePatchColor(img, image.Rect(bounds.Min.X, bounds.Min.Y, minInt(bounds.Min.X+patch, bounds.Max.X), minInt(bounds.Min.Y+patch, bounds.Max.Y))),
		averagePatchColor(img, image.Rect(maxInt(bounds.Max.X-patch, bounds.Min.X), bounds.Min.Y, bounds.Max.X, minInt(bounds.Min.Y+patch, bounds.Max.Y))),
		averagePatchColor(img, image.Rect(bounds.Min.X, maxInt(bounds.Max.Y-patch, bounds.Min.Y), minInt(bounds.Min.X+patch, bounds.Max.X), bounds.Max.Y)),
		averagePatchColor(img, image.Rect(maxInt(bounds.Max.X-patch, bounds.Min.X), maxInt(bounds.Max.Y-patch, bounds.Min.Y), bounds.Max.X, bounds.Max.Y)),
	}
	best := samples[len(samples)-1]
	bestCount := -1
	for _, sample := range samples {
		count := 0
		for _, other := range samples {
			if nearRGBA(sample, other, screenshotCornerClusterThreshold) {
				count++
			}
		}
		if count > bestCount {
			best = sample
			bestCount = count
		}
	}
	return best
}

func averagePatchColor(img image.Image, rect image.Rectangle) color.RGBA {
	if rect.Empty() {
		return color.RGBA{A: 255}
	}
	var r, g, b, a uint64
	var n uint64
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			cr, cg, cb, ca := rgba8(img.At(x, y))
			r += uint64(cr)
			g += uint64(cg)
			b += uint64(cb)
			a += uint64(ca)
			n++
		}
	}
	return color.RGBA{R: uint8(r / n), G: uint8(g / n), B: uint8(b / n), A: uint8(a / n)}
}

func nearColor(c color.Color, bg color.RGBA, threshold uint8) bool {
	r, g, b, a := rgba8(c)
	return nearRGBA(color.RGBA{R: r, G: g, B: b, A: a}, bg, threshold)
}

func nearRGBA(a, b color.RGBA, threshold uint8) bool {
	return absDiff(a.R, b.R) <= threshold && absDiff(a.G, b.G) <= threshold && absDiff(a.B, b.B) <= threshold && absDiff(a.A, b.A) <= threshold
}

func rgba8(c color.Color) (uint8, uint8, uint8, uint8) {
	r, g, b, a := c.RGBA()
	return uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)
}

func absDiff(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (a *App) Screenshot(previewID, service, path string) (map[string]any, error) {
	return a.ScreenshotWithOptions(previewID, service, ScreenshotOptions{Path: path})
}

func (a *App) ScreenshotWithOptions(previewID, service string, opts ScreenshotOptions) (map[string]any, error) {
	opts = normalizeScreenshotOptions(opts)
	if err := validateColorScheme(opts.ColorScheme); err != nil {
		return nil, err
	}
	p, err := a.getPreview(previewID)
	if err != nil {
		return nil, err
	}
	svc, ok := p.Services[service]
	if !ok {
		return nil, fmt.Errorf("service not found: %s", service)
	}
	projectBreakpoints := []ScreenshotBreakpoint{}
	if opts.UseProjectBreakpoints {
		if rec, err := a.getProject(p.Project); err == nil {
			projectBreakpoints = rec.Config.Agent.ScreenshotBreakpoints
		}
	}
	breakpoints := screenshotBreakpoints(opts, projectBreakpoints)
	if len(breakpoints) == 0 {
		breakpoints = []ScreenshotBreakpoint{{Width: opts.Width, Height: opts.Height}}
	}
	if _, err := exec.LookPath("npx"); err != nil {
		return nil, fmt.Errorf("npx/playwright not available for screenshots: %w", err)
	}
	baseURL := serviceBaseURLForTarget(svc, opts.Target)
	if baseURL == "" {
		return nil, fmt.Errorf("service %s has no %s URL", service, opts.Target)
	}
	url := strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(opts.Path, "/")
	screenshots := []map[string]any{}
	for _, bp := range breakpoints {
		if bp.Width <= 0 || bp.Height <= 0 {
			return nil, fmt.Errorf("invalid screenshot breakpoint %q: %dx%d", bp.Name, bp.Width, bp.Height)
		}
		out := screenshotOutputPath(a.Home, opts.OutputDir, previewID, service, opts.Path, bp, len(breakpoints) > 1, opts.ColorScheme)
		if err := ensureDir(filepath.Dir(out)); err != nil {
			return nil, err
		}
		args := []string{"--yes", "playwright", "screenshot", "--viewport-size", fmt.Sprintf("%d,%d", bp.Width, bp.Height)}
		args = append(args, "--channel", "chrome")
		if opts.FullPage {
			args = append(args, "--full-page")
		}
		if opts.WaitForSelector != "" {
			args = append(args, "--wait-for-selector", opts.WaitForSelector)
		}
		if opts.WaitForTimeout != "" {
			args = append(args, "--wait-for-timeout", opts.WaitForTimeout)
		}
		if opts.ColorScheme != "" {
			args = append(args, "--color-scheme", opts.ColorScheme)
		}
		args = append(args, url, out)
		cmd := exec.Command("npx", args...)
		b, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("playwright screenshot failed: %w: %s", err, string(b))
		}
		width, height, err := screenshotDimensions(out)
		if err != nil {
			return nil, fmt.Errorf("read screenshot dimensions: %w", err)
		}
		cropped := false
		originalWidth, originalHeight := width, height
		if opts.Crop {
			crop, err := cropScreenshotOuterWhitespace(out)
			if err != nil {
				return nil, fmt.Errorf("crop screenshot whitespace: %w", err)
			}
			cropped = crop.Cropped
			width = crop.Width
			height = crop.Height
			originalWidth = crop.OriginalWidth
			originalHeight = crop.OriginalHeight
		}
		screenshots = append(screenshots, map[string]any{
			"preview":        previewID,
			"service":        service,
			"target":         opts.Target,
			"colorScheme":    opts.ColorScheme,
			"url":            url,
			"path":           out,
			"breakpoint":     bp.Name,
			"viewportWidth":  bp.Width,
			"viewportHeight": bp.Height,
			"cropped":        cropped,
			"width":          width,
			"height":         height,
			"originalWidth":  originalWidth,
			"originalHeight": originalHeight,
		})
	}
	result := map[string]any{"preview": previewID, "service": service, "target": opts.Target, "url": url, "screenshots": screenshots}
	if len(screenshots) == 1 {
		for k, v := range screenshots[0] {
			result[k] = v
		}
	}
	return result, nil
}
