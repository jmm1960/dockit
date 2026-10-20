package dockit

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/containrrr/watchtower/internal/actions"
	wtcontainer "github.com/containrrr/watchtower/pkg/container"
	wt "github.com/containrrr/watchtower/pkg/types"
	"github.com/docker/docker/api/types"
	dc "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/robfig/cron"
	log "github.com/sirupsen/logrus"
)

type engine interface {
	list(context.Context) ([]wt.Container, error)
	update(wt.Container, time.Duration) (wt.Report, error)
	restart(context.Context, wt.Container, time.Duration) error
}

type dockerEngine struct {
	updater wtcontainer.Client
	api     *client.Client
}

func (d dockerEngine) list(context.Context) ([]wt.Container, error) {
	return d.updater.ListContainers(func(wt.FilterableContainer) bool { return true })
}
func (d dockerEngine) update(c wt.Container, timeout time.Duration) (wt.Report, error) {
	// Only this exact container is eligible; the legacy CLI's global filters are not used.
	return actions.Update(d.updater, wt.UpdateParams{Filter: func(candidate wt.FilterableContainer) bool {
		actual, ok := candidate.(wt.Container)
		return ok && actual.ID() == c.ID()
	}, Timeout: timeout})
}
func (d dockerEngine) restart(ctx context.Context, c wt.Container, timeout time.Duration) error {
	current, err := d.api.ContainerInspect(ctx, string(c.ID()))
	if err != nil {
		return err
	}
	if current.State == nil || !current.State.Running || current.State.Restarting {
		return fmt.Errorf("container is no longer running")
	}
	seconds := int(timeout / time.Second)
	return d.api.ContainerRestart(ctx, string(c.ID()), dc.StopOptions{Timeout: &seconds})
}

type task struct {
	spec     string
	schedule cron.Schedule
	next     time.Time
}

func (t *task) configure(spec string, now time.Time) {
	if t.spec == spec {
		return
	}
	t.spec = spec
	if spec == "" {
		t.schedule = nil
		t.next = time.Time{}
		return
	}
	t.schedule, _ = parseSchedule(spec)
	t.next = t.schedule.Next(now)
}
func (t *task) due(now time.Time) bool {
	if t.schedule == nil || now.Before(t.next) {
		return false
	}
	// Do not replay every missed occurrence after slow operations.
	t.next = t.schedule.Next(now)
	return true
}

type containerState struct {
	id              wt.ContainerID
	update, restart task
	unhealthySince  time.Time
	attempts        []time.Time
	lastAttempt     time.Time
	limitNotified   bool
}

type service struct {
	config       config
	engine       engine
	states       map[string]*containerState
	notify       func(string, string)
	selfHostname string
}

func Run(ctx context.Context) error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	b, err := newBark(c)
	if err != nil {
		return err
	}
	api, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return err
	}
	defer api.Close()
	hostname, _ := os.Hostname()
	s := service{config: c, engine: dockerEngine{wtcontainer.NewClient(wtcontainer.ClientOptions{}), api}, states: make(map[string]*containerState), notify: b.notify, selfHostname: hostname}
	go b.run(ctx)
	log.WithFields(log.Fields{"timezone": c.location.String(), "update_cron": c.updateCron}).Info("Dockit started; only opted-in containers are managed")
	if c.barkKey == "" {
		log.Info("Bark disabled: DOCKIT_BARK_KEY is empty")
	}
	ticker := time.NewTicker(c.poll)
	defer ticker.Stop()
	for {
		if err = ctx.Err(); err != nil {
			return nil
		}
		containers, listErr := s.engine.list(ctx)
		if listErr != nil {
			log.WithError(listErr).Warn("Docker discovery failed; retrying on next poll")
		} else {
			s.reconcile(ctx, containers, time.Now().In(c.location))
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (s *service) reconcile(ctx context.Context, containers []wt.Container, now time.Time) {
	seen := make(map[string]bool)
	for _, c := range containers {
		if ctx.Err() != nil {
			return
		}
		labels := c.ContainerInfo().Config.Labels
		// Own image is marked by Dockerfile. Hostname matching also covers local builds
		// using the default Docker hostname or an explicitly configured hostname.
		if labels["dockit.self"] == "true" || c.IsWatchtower() || (s.selfHostname != "" && (strings.HasPrefix(string(c.ID()), s.selfHostname) || c.ContainerInfo().Config.Hostname == s.selfHostname)) {
			continue
		}
		p, err := parsePolicy(labels, s.config)
		if err != nil {
			log.WithField("container", c.Name()).WithError(err).Warn("Ignoring invalid container policy")
			continue
		}
		if !p.enabled || !c.IsRunning() || c.IsRestarting() {
			continue
		}
		name := c.Name()
		seen[name] = true
		state := s.states[name]
		if state == nil {
			state = &containerState{}
			s.states[name] = state
		}
		if state.id != c.ID() {
			state.id = c.ID()
			state.unhealthySince = time.Time{}
		}
		updateSpec := ""
		if p.update {
			updateSpec = p.updateCron
		}
		state.update.configure(updateSpec, now)
		state.restart.configure(p.restartCron, now)
		updateDue, restartDue := state.update.due(now), state.restart.due(now)
		if updateDue {
			report, err := s.engine.update(c, s.config.stopTimeout)
			if err != nil {
				log.WithError(err).WithField("container", name).Error("Update failed")
				s.notify("更新失败", name+"：更新操作失败，请查看日志")
			} else if report != nil {
				for _, r := range report.All() {
					switch r.State() {
					case "Updated":
						s.notify("更新成功", fmt.Sprintf("%s：%s → %s（启动请求成功）", name, r.CurrentImageID().ShortID(), r.LatestImageID().ShortID()))
					case "Failed", "Skipped":
						s.notify("更新失败", name+"：更新失败或已跳过，请查看日志")
					}
				}
			}
			// Snapshot may now refer to a removed container. Refresh before other work.
			continue
		}
		if restartDue {
			s.restart(ctx, c, "定时重启")
			continue
		}
		if p.heal {
			s.heal(ctx, c, p, state, now)
		} else {
			state.unhealthySince = time.Time{}
		}
	}
	for name := range s.states {
		if !seen[name] {
			delete(s.states, name)
		}
	}
}

func (s *service) restart(ctx context.Context, c wt.Container, reason string) {
	operationCtx, cancel := context.WithTimeout(ctx, s.config.stopTimeout+30*time.Second)
	defer cancel()
	if err := s.engine.restart(operationCtx, c, s.config.stopTimeout); err != nil {
		log.WithError(err).WithField("container", c.Name()).Error(reason + "失败")
		s.notify(reason+"失败", c.Name()+"：请查看日志")
	} else {
		s.notify(reason, c.Name()+"：重启请求成功")
		log.WithField("container", c.Name()).Info(reason + "请求成功")
	}
}

func (s *service) heal(ctx context.Context, c wt.Container, p policy, state *containerState, now time.Time) {
	health := c.ContainerInfo().State.Health
	if health == nil || health.Status != types.Unhealthy {
		state.unhealthySince = time.Time{}
		return
	}
	if state.unhealthySince.IsZero() {
		state.unhealthySince = now
	}
	if now.Sub(state.unhealthySince) < p.unhealthyFor {
		return
	}
	kept := state.attempts[:0]
	for _, at := range state.attempts {
		if now.Sub(at) < p.window {
			kept = append(kept, at)
		}
	}
	state.attempts = kept
	if len(kept) >= p.maxRestarts {
		if !state.limitNotified {
			s.notify("健康恢复暂停", c.Name()+"：达到窗口内重启次数上限")
			state.limitNotified = true
		}
		return
	}
	state.limitNotified = false
	if !state.lastAttempt.IsZero() && now.Sub(state.lastAttempt) < p.cooldown {
		return
	}
	state.lastAttempt = now
	state.attempts = append(state.attempts, now)
	state.unhealthySince = time.Time{}
	s.restart(ctx, c, "健康恢复重启")
}
