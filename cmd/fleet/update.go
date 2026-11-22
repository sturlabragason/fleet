package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/prefs"
	"github.com/sturlabragason/fleet/internal/update"
)

// updateDelay is how long a fresh watcher waits before its first update
// pass, so a start or re-exec is never slowed by the network.
const updateDelay = time.Minute

func newUpdater(f *fleet.Fleet, cfg prefs.Update) *update.Updater {
	u := &update.Updater{
		Home:         f.Home,
		Exe:          f.Exe,
		Current:      version,
		Every:        cfg.Every,
		BuildCommand: cfg.BuildCommand,
	}
	if cfg.WaitQuiet {
		u.Quiet = f.UpgradeQuiet
	}
	return u
}

// startUpdater runs the release auto-upgrade in the resident watcher
// (ADR-114) unless config.toml turns it off or this build is not eligible.
func startUpdater(ctx context.Context, f *fleet.Fleet) {
	log := logx.For("update")
	cfg, err := prefs.LoadUpdate()
	if err != nil {
		log.Error("config.toml", "err", err)
	}
	if !cfg.Auto {
		log.Info("auto-upgrade off", "reason", "[update] auto = false")
		return
	}
	if ok, why := update.EligibleFor(version, channel, cfg.BuildCommand); !ok {
		log.Info("auto-upgrade off", "reason", why)
		return
	}
	go newUpdater(f, cfg).Run(ctx, updateDelay)
}

// cmdUpdate shows the auto-upgrade state; --check asks for the latest
// release now and --now installs it at once.
func cmdUpdate(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	check := fs.Bool("check", false, "ask GitHub for the latest release now")
	now := fs.Bool("now", false, "install the latest release now, without waiting for idle agents")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("usage: fleet update [--check | --now]")
	}
	cfg, cfgErr := prefs.LoadUpdate()
	ok, why := update.EligibleFor(version, channel, cfg.BuildCommand)
	u := newUpdater(f, cfg)
	ctx := context.Background()
	switch {
	case *now:
		if !ok {
			return fmt.Errorf("not upgrading: %s; install a release with install.sh or set [update] build_command", why)
		}
		v, err := u.Install(ctx)
		if err != nil {
			return fmt.Errorf("upgrade failed, %s kept: %w", version, err)
		}
		if v == "" {
			fmt.Printf("fleet %s is the latest release\n", version)
			return nil
		}
		fmt.Printf("upgraded %s → %s (%s); running fleet processes follow it. fleet changes shows what's new\n", version, v, f.Exe)
		return nil
	case *check:
		latest, err := u.Latest(ctx)
		if err != nil {
			return err
		}
		if update.Newer(latest, version) {
			fmt.Printf("fleet %s is out (running %s)\n", latest, version)
		} else {
			fmt.Printf("fleet %s is the latest release (running %s)\n", latest, version)
		}
		return nil
	}
	printUpdateStatus(f, cfg, cfgErr, ok, why)
	return nil
}

func printUpdateStatus(f *fleet.Fleet, cfg prefs.Update, cfgErr error, ok bool, why string) {
	fmt.Printf("running:   %s\n", versionLine(version, moduleVersion()))
	switch {
	case cfgErr != nil:
		fmt.Printf("auto:      on (config.toml: %v)\n", cfgErr)
	case !cfg.Auto:
		fmt.Println("auto:      off ([update] auto = false)")
	case !ok:
		fmt.Printf("auto:      off (%s)\n", why)
	default:
		when := "applied when no agent is working"
		if !cfg.WaitQuiet {
			when = "applied immediately"
		}
		fmt.Printf("auto:      on, every %s, %s\n", cfg.Every, when)
	}
	switch {
	case cfg.BuildCommand != "":
		fmt.Println("verified:  local build command + binary probe")
	case update.VerifySums != nil:
		fmt.Println("verified:  signed checksums")
	default:
		if why := update.Provenance(); why == "" {
			fmt.Println("verified:  checksum and gh attestation")
		} else {
			fmt.Printf("verified:  checksum only (%s)\n", why)
		}
	}
	s := update.Load(f.Home)
	if !s.CheckedAt.IsZero() {
		fmt.Printf("checked:   %s (latest %s)\n", s.CheckedAt.Local().Format(time.DateTime), orDash(s.Latest))
	}
	if s.Staged != "" {
		wait := "waiting for idle agents"
		if !cfg.WaitQuiet {
			wait = "waiting for next pass"
		}
		fmt.Printf("staged:    %s, %s\n", s.Staged, wait)
	}
	if s.Failed != "" {
		fmt.Printf("failed:    %s at %s: %s\n", s.Failed, s.FailedAt.Local().Format(time.DateTime), s.Err)
	}
	if a := s.Applied; a != nil {
		fmt.Printf("upgraded:  %s → %s at %s\n", a.From, a.To, a.At.Local().Format(time.DateTime))
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
