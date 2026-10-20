package dockit

import (
	"context"
	"testing"
	"time"

	wc "github.com/containrrr/watchtower/pkg/container"
	wt "github.com/containrrr/watchtower/pkg/types"
	"github.com/docker/docker/api/types"
	dc "github.com/docker/docker/api/types/container"
)

type fakeEngine struct{ updates, restarts []wt.ContainerID }

func (f *fakeEngine) list(context.Context) ([]wt.Container, error) { return nil, nil }
func (f *fakeEngine) update(c wt.Container, _ time.Duration) (wt.Report, error) {
	f.updates = append(f.updates, c.ID())
	return nil, nil
}
func (f *fakeEngine) restart(_ context.Context, c wt.Container, _ time.Duration) error {
	f.restarts = append(f.restarts, c.ID())
	return nil
}

func testContainer(id, name string, labels map[string]string, status string) wt.Container {
	return wc.NewContainer(&types.ContainerJSON{ContainerJSONBase: &types.ContainerJSONBase{ID: id, Name: name, Image: "sha256:old", State: &types.ContainerState{Running: true, Health: &types.Health{Status: status}}, HostConfig: &dc.HostConfig{}}, Config: &dc.Config{Image: "nginx:stable", Labels: labels}}, &types.ImageInspect{ID: "sha256:old", Config: &dc.Config{}})
}
func testService() (*service, *fakeEngine, *[]notification) {
	f := &fakeEngine{}
	events := []notification{}
	return &service{config: config{updateCron: "0 4 * * *", location: time.UTC, stopTimeout: time.Second}, engine: f, states: make(map[string]*containerState), notify: func(title, body string) { events = append(events, notification{title, body}) }}, f, &events
}

func TestPolicyInheritanceAndOverrides(t *testing.T) {
	defaults := config{updateCron: "0 4 * * *"}
	for _, tc := range []struct {
		name            string
		labels          map[string]string
		enabled, update bool
		cron            string
		invalid         bool
	}{
		{"opt in", map[string]string{"dockit.enable": "true", "dockit.update.enable": "true"}, true, true, "0 4 * * *", false},
		{"override", map[string]string{"dockit.enable": "true", "dockit.update.enable": "true", "dockit.update.cron": "0 6 * * *"}, true, true, "0 6 * * *", false},
		{"cron does not opt in", map[string]string{"dockit.update.cron": "0 6 * * *"}, false, false, "0 4 * * *", false},
		{"legacy", map[string]string{legacyEnable: "true"}, true, true, "0 4 * * *", false},
		{"explicit update disable", map[string]string{legacyEnable: "true", "dockit.update.enable": "false"}, true, false, "0 4 * * *", false},
		{"master disable", map[string]string{legacyEnable: "true", "dockit.enable": "false"}, false, false, "0 4 * * *", false},
		{"bad cron", map[string]string{"dockit.enable": "true", "dockit.update.enable": "true", "dockit.update.cron": "invalid"}, false, false, "", true},
		{"bad boolean", map[string]string{"dockit.enable": "maybe"}, false, false, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := parsePolicy(tc.labels, defaults)
			if tc.invalid {
				if err == nil {
					t.Fatal("expected rejection")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if p.enabled != tc.enabled || p.update != tc.update || p.updateCron != tc.cron {
				t.Fatalf("unexpected policy: %+v", p)
			}
		})
	}
}

func TestIndependentSchedulesAndReplacement(t *testing.T) {
	s, f, _ := testService()
	ctx := context.Background()
	now := time.Date(2026, 10, 20, 3, 59, 0, 0, time.UTC)
	labels := map[string]string{"dockit.enable": "true", "dockit.update.enable": "true"}
	a := testContainer("a", "/a", labels, types.Healthy)
	b := testContainer("b", "/b", map[string]string{"dockit.enable": "true", "dockit.update.enable": "true", "dockit.update.cron": "0 5 * * *"}, types.Healthy)
	s.reconcile(ctx, []wt.Container{a, b}, now)
	s.reconcile(ctx, []wt.Container{a, b}, now.Add(time.Minute))
	if len(f.updates) != 1 || f.updates[0] != "a" {
		t.Fatalf("updates: %v", f.updates)
	}
	a = testContainer("replacement-a", "/a", labels, types.Healthy)
	s.reconcile(ctx, []wt.Container{a, b}, now.Add(2*time.Minute))
	if len(f.updates) != 1 {
		t.Fatal("replacement duplicated update")
	}
	s.reconcile(ctx, []wt.Container{a, b}, now.Add(61*time.Minute))
	if len(f.updates) != 2 || f.updates[1] != "b" {
		t.Fatalf("updates: %v", f.updates)
	}
	s.reconcile(ctx, nil, now.Add(62*time.Minute))
	if len(s.states) != 0 {
		t.Fatal("removed container tasks retained")
	}
}

func TestUpdateTakesPriorityOverRestartAndHeal(t *testing.T) {
	s, f, _ := testService()
	now := time.Date(2026, 10, 20, 3, 59, 0, 0, time.UTC)
	c := testContainer("a", "/a", map[string]string{"dockit.enable": "true", "dockit.update.enable": "true", "dockit.restart.cron": "0 4 * * *", "dockit.heal.enable": "true", "dockit.heal.unhealthy-for": "2m"}, types.Unhealthy)
	s.reconcile(context.Background(), []wt.Container{c}, now)
	s.reconcile(context.Background(), []wt.Container{c}, now.Add(time.Minute))
	if len(f.updates) != 1 || len(f.restarts) != 0 {
		t.Fatalf("overlapping actions: %+v", f)
	}
}

func TestHealThresholdCooldownLimitAndWindow(t *testing.T) {
	s, f, events := testService()
	ctx := context.Background()
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	c := testContainer("a", "/a", map[string]string{"dockit.enable": "true", "dockit.heal.enable": "true", "dockit.heal.unhealthy-for": "2m", "dockit.heal.cooldown": "10m", "dockit.heal.max-restarts": "2", "dockit.heal.window": "1h"}, types.Unhealthy)
	for _, minutes := range []int{0, 1, 2, 3, 4, 11, 12, 13, 14, 22, 23} {
		s.reconcile(ctx, []wt.Container{c}, now.Add(time.Duration(minutes)*time.Minute))
	}
	if len(f.restarts) != 2 {
		t.Fatalf("restarts: %v", f.restarts)
	}
	paused := 0
	for _, n := range *events {
		if n.Title == "健康恢复暂停" {
			paused++
		}
	}
	if paused != 1 {
		t.Fatalf("pause notifications: %d", paused)
	}
	s.reconcile(ctx, []wt.Container{c}, now.Add(63*time.Minute))
	if len(f.restarts) != 3 {
		t.Fatal("window did not expire")
	}
}

func TestOnlyRunningOptedInContainersAreManaged(t *testing.T) {
	s, f, _ := testService()
	now := time.Now()
	labels := map[string]string{"dockit.enable": "true", "dockit.heal.enable": "true"}
	stopped := testContainer("stopped", "/stopped", labels, types.Unhealthy)
	stopped.ContainerInfo().State.Running = false
	self := testContainer("self", "/dockit", map[string]string{"dockit.self": "true", "dockit.enable": "true", "dockit.heal.enable": "true"}, types.Unhealthy)
	unmanaged := testContainer("other", "/other", nil, types.Unhealthy)
	s.reconcile(context.Background(), []wt.Container{stopped, self, unmanaged}, now)
	if len(f.restarts) != 0 || len(f.updates) != 0 || len(s.states) != 0 {
		t.Fatal("managed an excluded container")
	}
}

func TestScheduledRestartAndHealthyReset(t *testing.T) {
	s, f, _ := testService()
	ctx := context.Background()
	now := time.Date(2026, 10, 20, 3, 59, 0, 0, time.UTC)
	c := testContainer("a", "/a", map[string]string{"dockit.enable": "true", "dockit.restart.cron": "0 4 * * *", "dockit.heal.enable": "true", "dockit.heal.unhealthy-for": "2m"}, types.Unhealthy)
	s.reconcile(ctx, []wt.Container{c}, now)
	s.reconcile(ctx, []wt.Container{c}, now.Add(time.Minute))
	if len(f.restarts) != 1 {
		t.Fatal("scheduled restart not executed")
	}
	c.ContainerInfo().State.Health.Status = types.Healthy
	s.reconcile(ctx, []wt.Container{c}, now.Add(2*time.Minute))
	c.ContainerInfo().State.Health.Status = types.Unhealthy
	s.reconcile(ctx, []wt.Container{c}, now.Add(3*time.Minute))
	if len(f.restarts) != 1 {
		t.Fatal("healthy interval did not reset threshold")
	}
	s.reconcile(ctx, []wt.Container{c}, now.Add(5*time.Minute))
	if len(f.restarts) != 2 {
		t.Fatal("continuous unhealthy interval did not trigger heal")
	}
}
