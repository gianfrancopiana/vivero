package vivero

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type qaRecordRunner interface {
	LookPath(file string) (string, error)
	Run(name string, args ...string) ([]byte, []byte, error)
	CombinedOutput(name string, args ...string) ([]byte, error)
}

type osQARecordRunner struct{}

func (osQARecordRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (osQARecordRunner) Run(name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func (osQARecordRunner) CombinedOutput(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

func (a *App) recordRunner() qaRecordRunner {
	if a != nil && a.qaRecordRunner != nil {
		return a.qaRecordRunner
	}
	return osQARecordRunner{}
}

func (a *App) QARecord(previewID string, opts QARecordOptions) (map[string]any, error) {
	opts = normalizeQARecordOptions(opts)
	if err := validateColorScheme(opts.ColorScheme); err != nil {
		return nil, err
	}
	if opts.Format != "mp4" && opts.Format != "webm" {
		return nil, fmt.Errorf("qa record format must be mp4 or webm")
	}
	plan, err := a.QAPlanWithTarget(previewID, opts.Scope, opts.Target)
	if err != nil {
		return nil, err
	}
	artifacts, _ := plan["artifacts"].(map[string]any)
	artifactDir := stringValue(artifacts["dir"])
	outputDir := expandPath(opts.OutputDir)
	if outputDir == "" {
		if dir := stringValue(artifacts["videoDir"]); dir != "" {
			outputDir = expandPath(dir)
		} else if artifactDir != "" {
			outputDir = filepath.Join(expandPath(artifactDir), "videos")
		}
	}
	if outputDir == "" {
		outputDir = qaVideoFallbackDir(a.Home, previewID)
	}
	if err := ensureDir(outputDir); err != nil {
		return nil, err
	}
	runner := a.recordRunner()
	if _, err := runner.LookPath("npm"); err != nil {
		return nil, fmt.Errorf("npm/playwright not available for qa recording: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "vivero-qa-record-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	inputPath := filepath.Join(tmpDir, "input.json")
	scriptPath := filepath.Join(tmpDir, "record.js")
	payload := map[string]any{"plan": plan, "options": opts, "outputDir": outputDir}
	if err := writeIndentedJSONFile(inputPath, payload, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(scriptPath, []byte(qaRecordPlaywrightScript), 0o644); err != nil {
		return nil, err
	}

	stdout, stderr, err := runner.Run("npm", "exec", "--yes", "--package", playwrightPackage(), "--", "sh", "-lc", `NODE_PATH="$(dirname "$(dirname "$(command -v playwright)")")" exec node "$1" "$2"`, "vivero-playwright", scriptPath, inputPath)
	if err != nil {
		return nil, fmt.Errorf("playwright qa record failed: %w: %s", err, strings.TrimSpace(string(stderr)+"\n"+string(stdout)))
	}

	var result map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &result); err != nil {
		return nil, fmt.Errorf("parse qa record output: %w: %s", err, strings.TrimSpace(string(stdout)))
	}
	result["preview"] = previewID
	result["scope"] = scopeNameFromPlan(plan)
	result["target"] = opts.Target
	result["colorScheme"] = opts.ColorScheme
	result["storageState"] = opts.StorageState
	result["format"] = opts.Format
	result["outputDir"] = outputDir
	result["plan"] = plan
	if opts.Format == "mp4" {
		if err := convertRecordVideosToMP4(runner, result); err != nil {
			result["ok"] = false
			result["conversionError"] = err.Error()
			return result, err
		}
	}
	if recordPath, err := writeQARecordResult(artifacts, result); err != nil {
		result["ok"] = false
		result["recordArtifactError"] = err.Error()
	} else if recordPath != "" {
		result["recordPath"] = recordPath
	}
	return result, nil
}

func convertRecordVideosToMP4(runner qaRecordRunner, result map[string]any) error {
	if _, err := runner.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not available for mp4 conversion: %w", err)
	}
	videos, _ := result["videos"].([]any)
	for _, raw := range videos {
		video, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		webm := stringValue(video["path"])
		if webm == "" {
			continue
		}
		mp4 := strings.TrimSuffix(webm, filepath.Ext(webm)) + ".mp4"
		b, err := runner.CombinedOutput("ffmpeg", "-y", "-i", webm, "-movflags", "+faststart", "-pix_fmt", "yuv420p", mp4)
		if err != nil {
			return fmt.Errorf("ffmpeg convert %s: %w: %s", webm, err, strings.TrimSpace(string(b)))
		}
		video["sourcePath"] = webm
		video["path"] = mp4
		video["format"] = "mp4"
	}
	return nil
}

func writeQARecordResult(artifacts map[string]any, result map[string]any) (string, error) {
	recordPath := stringValue(artifacts["recordPath"])
	if recordPath == "" {
		return "", nil
	}
	recordPath = expandPath(recordPath)
	if !filepath.IsAbs(recordPath) {
		if dir := stringValue(artifacts["dir"]); dir != "" {
			recordPath = filepath.Join(dir, recordPath)
		}
	}
	if err := ensureDir(filepath.Dir(recordPath)); err != nil {
		return "", err
	}
	if err := writeIndentedJSONFile(recordPath, result, 0o644); err != nil {
		return "", err
	}
	return recordPath, nil
}

const qaRecordPlaywrightScript = `
const fs = require('fs');
const path = require('path');
const { chromium } = require('playwright');

const input = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
const plan = input.plan || {};
const opts = input.options || {};
const outputDir = input.outputDir;

function safeName(value) {
  const text = String(value || 'unnamed').trim() || 'unnamed';
  return text.replace(/[^a-zA-Z0-9._-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 80) || 'unnamed';
}

function pageFlowsForScope(scope) {
  const flows = Array.isArray(scope.flows) ? scope.flows.slice() : [];
  if (flows.length > 0) return flows;
  return (scope.pages || []).map((page) => ({
    name: page.name || page.path || 'page',
    start: page,
    steps: [{ visit: page.name || page.path || '/', url: page.url, path: page.path, service: page.service }]
  }));
}

async function run() {
  fs.mkdirSync(outputDir, { recursive: true });
  const browser = await chromium.launch({ channel: 'chrome', headless: true, slowMo: opts.slowMoMs || 0 });
  const videos = [];
  let ok = true;
  try {
    for (const scope of (plan.scopes || [])) {
      for (const flow of pageFlowsForScope(scope)) {
        const flowName = safeName(flow.name || 'flow');
        const scopeName = safeName(scope.name || 'scope');
        const flowDir = path.join(outputDir, scopeName, flowName);
        fs.mkdirSync(flowDir, { recursive: true });
        const contextOptions = {
          viewport: { width: opts.width || 1280, height: opts.height || 800 },
          deviceScaleFactor: opts.deviceScaleFactor || 1,
          colorScheme: opts.colorScheme || undefined,
          ignoreHTTPSErrors: true,
          recordVideo: { dir: flowDir, size: { width: opts.width || 1280, height: opts.height || 800 } }
        };
        const storageState = opts.storageState || scope.storageState || '';
        if (storageState) contextOptions.storageState = storageState;
        const context = await browser.newContext(contextOptions);
        const page = await context.newPage();
        const steps = [];
        const errors = [];
        let firstURL = (flow.start && flow.start.url) || '';
        try {
          if (firstURL) {
            await page.goto(firstURL, { waitUntil: 'domcontentloaded' });
            steps.push({ action: 'start', url: page.url() });
            if (opts.waitMs) await page.waitForTimeout(opts.waitMs);
          }
          for (const step of (flow.steps || [])) {
            const stepOut = { ...step };
            if (step.url) {
              await page.goto(step.url, { waitUntil: 'domcontentloaded' });
              stepOut.action = 'visit';
              stepOut.currentUrl = page.url();
            }
            if (step.click) {
              await page.locator(step.click).first().click({ timeout: 5000 });
              stepOut.action = 'click';
            }
            if (step.fill) {
              await page.locator(step.fill).first().fill(step.value || '', { timeout: 5000 });
              stepOut.action = 'fill';
            }
            if (step.press) {
              await page.keyboard.press(step.press);
              stepOut.action = 'press';
            }
            if (step.expectText) {
              await page.getByText(step.expectText).first().waitFor({ timeout: 5000 });
              stepOut.expectTextFound = true;
            }
            if (step.expectUrl) {
              const current = page.url();
              if (!current.includes(step.expectUrl)) throw new Error('expected URL to contain ' + step.expectUrl + ', got ' + current);
              stepOut.expectUrlMatched = true;
            }
            if (step.screenshot) {
              const screenshotPath = path.join(flowDir, safeName(step.screenshot) + '.png');
              await page.screenshot({ path: screenshotPath, fullPage: false });
              stepOut.screenshotPath = screenshotPath;
            }
            if (opts.waitMs) await page.waitForTimeout(opts.waitMs);
            steps.push(stepOut);
          }
        } catch (error) {
          ok = false;
          errors.push(error.message || String(error));
        }
        const video = page.video();
        await context.close();
        const videoPath = video ? await video.path() : '';
        videos.push({
          scope: scope.name || '',
          name: flow.name || '',
          ok: errors.length === 0,
          colorScheme: opts.colorScheme || '',
          authSession: scope.authSession || '',
          storageState,
          url: firstURL,
          path: videoPath,
          format: 'webm',
          steps,
          errors
        });
      }
    }
  } finally {
    await browser.close();
  }
  process.stdout.write(JSON.stringify({ ok, videos }, null, 2));
}

run().catch((error) => {
  console.error(error && error.stack ? error.stack : error);
  process.exit(1);
});
`
