package dockit

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron"
)

const legacyEnable = "com.centurylinklabs.watchtower.enable"

type config struct {
	updateCron                     string
	poll                           time.Duration
	stopTimeout                    time.Duration
	location                       *time.Location
	barkServer, barkKey, barkGroup string
}

func loadConfig() (config, error) {
	c := config{updateCron: env("DOCKIT_UPDATE_CRON", "0 4 * * *"), poll: 5 * time.Second, stopTimeout: 10 * time.Second,
		barkServer: env("DOCKIT_BARK_SERVER", "https://api.day.app"), barkKey: os.Getenv("DOCKIT_BARK_KEY"), barkGroup: env("DOCKIT_BARK_GROUP", "Dockit")}
	var err error
	c.location, err = time.LoadLocation(env("TZ", "UTC"))
	if err != nil {
		return c, fmt.Errorf("invalid TZ: %w", err)
	}
	if _, err = parseSchedule(c.updateCron); err != nil {
		return c, fmt.Errorf("invalid DOCKIT_UPDATE_CRON: %w", err)
	}
	for _, item := range []struct {
		name string
		dst  *time.Duration
	}{{"DOCKIT_POLL_INTERVAL", &c.poll}, {"DOCKIT_STOP_TIMEOUT", &c.stopTimeout}} {
		if value := os.Getenv(item.name); value != "" {
			*item.dst, err = time.ParseDuration(value)
			if err != nil || *item.dst < time.Second {
				return c, fmt.Errorf("%s must be a duration of at least 1s", item.name)
			}
		}
	}
	return c, nil
}

type policy struct {
	enabled, update, heal          bool
	updateCron, restartCron        string
	unhealthyFor, cooldown, window time.Duration
	maxRestarts                    int
}

func parsePolicy(labels map[string]string, defaults config) (policy, error) {
	p := policy{updateCron: defaults.updateCron, unhealthyFor: 0, cooldown: 10 * time.Minute, window: time.Hour, maxRestarts: 3}
	var err error
	flag := func(key string, fallback bool) bool {
		v, exists := labels[key]
		if !exists {
			return fallback
		}
		b, e := strconv.ParseBool(v)
		if e != nil {
			err = fmt.Errorf("invalid boolean label %s", key)
		}
		return b
	}
	legacy := flag(legacyEnable, false)
	p.enabled = flag("dockit.enable", legacy)
	if err != nil || !p.enabled {
		return p, err
	}
	p.update = flag("dockit.update.enable", legacy)
	p.heal = flag("dockit.heal.enable", false)
	if err != nil {
		return p, err
	}
	if v, ok := labels["dockit.update.cron"]; ok {
		p.updateCron = v
	}
	p.restartCron = labels["dockit.restart.cron"]
	for _, task := range []struct {
		enabled    bool
		key, value string
	}{{p.update, "dockit.update.cron", p.updateCron}, {p.restartCron != "", "dockit.restart.cron", p.restartCron}} {
		if task.enabled {
			if _, e := parseSchedule(task.value); e != nil {
				return p, fmt.Errorf("invalid %s: %w", task.key, e)
			}
		}
	}
	for _, item := range []struct {
		key  string
		dst  *time.Duration
		zero bool
	}{{"dockit.heal.unhealthy-for", &p.unhealthyFor, true}, {"dockit.heal.cooldown", &p.cooldown, false}, {"dockit.heal.window", &p.window, false}} {
		if v, ok := labels[item.key]; ok {
			d, e := time.ParseDuration(v)
			if e != nil || d < 0 || (!item.zero && d == 0) {
				return p, fmt.Errorf("invalid duration label %s", item.key)
			}
			*item.dst = d
		}
	}
	if v, ok := labels["dockit.heal.max-restarts"]; ok {
		p.maxRestarts, err = strconv.Atoi(v)
		if err != nil || p.maxRestarts < 1 {
			return p, fmt.Errorf("dockit.heal.max-restarts must be positive")
		}
	}
	return p, nil
}

// A single five-field cron syntax for both global and per-container schedules.
func parseSchedule(spec string) (cron.Schedule, error) {
	if len(strings.Fields(spec)) != 5 {
		return nil, fmt.Errorf("expected five cron fields")
	}
	return cron.ParseStandard(spec)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
