package vivero

import (
	"fmt"
	"sort"
)

const defaultStartupConcurrency = 4

type serviceStartResult struct {
	name    string
	service PreviewService
	err     error
}

func startupConcurrency(resources ResourceConfig, serviceCount int) int {
	if serviceCount <= 0 {
		return 0
	}
	limit := resources.MaxStartupConcurrency
	if limit == 0 {
		limit = defaultStartupConcurrency
	}
	if limit < 1 {
		limit = 1
	}
	if limit > serviceCount {
		limit = serviceCount
	}
	return limit
}

func startServicesBounded(names []string, concurrency int, start func(name string) (PreviewService, error)) (map[string]PreviewService, error) {
	ordered := append([]string(nil), names...)
	sort.Strings(ordered)
	results := make(map[string]PreviewService, len(ordered))
	if len(ordered) == 0 {
		return results, nil
	}
	if concurrency <= 0 {
		concurrency = defaultStartupConcurrency
	}
	if concurrency > len(ordered) {
		concurrency = len(ordered)
	}

	resultCh := make(chan serviceStartResult, concurrency)
	errs := make(map[string]error)
	next := 0
	inFlight := 0
	stopping := false
	for next < len(ordered) || inFlight > 0 {
		for !stopping && next < len(ordered) && inFlight < concurrency {
			name := ordered[next]
			next++
			inFlight++
			go func() {
				service, err := start(name)
				resultCh <- serviceStartResult{name: name, service: service, err: err}
			}()
		}
		if inFlight == 0 {
			break
		}
		result := <-resultCh
		inFlight--
		results[result.name] = result.service
		if result.err != nil {
			errs[result.name] = result.err
			stopping = true
		}
	}
	for _, name := range ordered {
		if err := errs[name]; err != nil {
			return results, err
		}
	}
	return results, nil
}

func (a *App) startAppServices(req UpRequest, cfg ProjectConfig, sources map[string]PreviewSource, services map[string]PreviewService) error {
	names := sortedMapKeys(cfg.Services)
	started, err := startServicesBounded(names, startupConcurrency(cfg.Resources, len(names)), func(name string) (PreviewService, error) {
		ps, err := a.startService(req, name, cfg.Services[name], sources, cfg, req.Public, true)
		if err != nil {
			a.recordEvent(req.ID, "error", "service.failed", err.Error(), name, nil)
			return ps, fmt.Errorf("start service %s: %w", name, err)
		}
		if err := a.saveService(req.ID, ps); err != nil {
			a.recordEvent(req.ID, "error", "service.failed", err.Error(), name, nil)
			return ps, fmt.Errorf("save service %s: %w", name, err)
		}
		return ps, nil
	})
	for _, name := range sortedMapKeys(started) {
		services[name] = started[name]
	}
	return err
}

func (a *App) startBackingServices(req UpRequest, cfg ProjectConfig, sources map[string]PreviewSource, services map[string]PreviewService) error {
	names := sortedMapKeys(cfg.BackingServices)
	started, err := startServicesBounded(names, startupConcurrency(cfg.Resources, len(names)), func(name string) (PreviewService, error) {
		svc := serviceConfigForBacking(cfg.BackingServices[name])
		ps, err := a.startService(req, name, svc, sources, cfg, false, false)
		if err != nil {
			return ps, fmt.Errorf("start backing service %s: %w", name, err)
		}
		if err := a.saveService(req.ID, ps); err != nil {
			return ps, fmt.Errorf("save backing service %s: %w", name, err)
		}
		return ps, nil
	})
	for _, name := range sortedMapKeys(started) {
		services[name] = started[name]
	}
	return err
}
