// Command quietbench runs a benchmark on macOS once the machine is quiet, keeps
// it on the performance cores and awake while it runs, and reports whether it
// stayed quiet.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type options struct {
	idle     float64
	settle   time.Duration
	timeout  time.Duration
	lowPower bool
}

func main() {
	os.Exit(run())
}

// run returns the exit code instead of exiting, so deferred cleanup such as
// restoring Low Power Mode always happens.
func run() int {
	var opts options
	flag.Float64Var(&opts.idle, "idle", 95, "percent of CPU time, across all cores, that must be idle before starting")
	flag.DurationVar(&opts.settle, "settle", 30*time.Second, "how long the machine must stay idle at nominal thermal pressure before starting")
	flag.DurationVar(&opts.timeout, "timeout", 5*time.Minute, "give up if the machine has not settled by then, 0 waits forever")
	flag.BoolVar(&opts.lowPower, "lowpower", false, "turn on Low Power Mode for the run and restore it afterwards, runs sudo pmset")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: quietbench [flags] command [args...]\n       quietbench            print the current conditions\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() == 0 {
		if err := status(); err != nil {
			logf("%v", err)
			return 1
		}
		return 0
	}

	code, err := bench(opts, flag.Args())
	if err != nil {
		logf("%v", err)
		if code == 0 {
			code = 1
		}
	}
	return code
}

func bench(opts options, args []string) (int, error) {
	// Catch signals from the start, so Ctrl-C while waiting still restores
	// Low Power Mode, and so they reach the benchmark once it runs.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)

	ac, err := onAC()
	if err != nil {
		return 1, fmt.Errorf("reading the power source: %w", err)
	}
	if !ac {
		return 1, errors.New("running on battery, plug in the charger")
	}

	th, err := openThermal()
	if err != nil {
		return 1, err
	}
	defer th.close()

	if opts.lowPower {
		on, err := lowPowerMode()
		if err != nil {
			return 1, err
		}
		if !on {
			logf("turning on Low Power Mode")
			if err := setLowPowerMode(true); err != nil {
				return 1, fmt.Errorf("turning on Low Power Mode: %w", err)
			}
			defer func() {
				logf("restoring Low Power Mode")
				if err := setLowPowerMode(false); err != nil {
					logf("restoring Low Power Mode failed, run 'sudo pmset -c lowpowermode 0': %v", err)
				}
			}()
		}
	}

	perf, all, err := cores()
	if err != nil {
		return 1, err
	}
	env := os.Environ()
	if _, set := os.LookupEnv("GOMAXPROCS"); !set && perf > 0 {
		env = append(env, "GOMAXPROCS="+strconv.Itoa(perf))
		logf("GOMAXPROCS=%d, the performance core count", perf)
	}

	if err := waitSettled(opts, th, sigs); err != nil {
		return 1, err
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = env

	before, err := readCPU()
	if err != nil {
		return 1, err
	}
	start := time.Now()
	logf("running %s", strings.Join(args, " "))
	if err := cmd.Start(); err != nil {
		return 1, err
	}

	// Keep the Mac from idle sleeping for as long as the benchmark runs.
	awake := exec.Command("caffeinate", "-i", "-w", strconv.Itoa(cmd.Process.Pid))
	if err := awake.Start(); err != nil {
		logf("warning: caffeinate did not start, the Mac may sleep: %v", err)
		awake = nil
	}

	done := make(chan struct{})
	peak := make(chan pressure, 1)
	go func() {
		highest := th.level()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-done:
				peak <- highest
				return
			case <-tick.C:
				if p := th.level(); p > highest {
					highest = p
				}
			case s := <-sigs:
				_ = cmd.Process.Signal(s)
			}
		}
	}()

	waitErr := cmd.Wait()
	wall := time.Since(start)
	close(done)
	maxPressure := <-peak
	if awake != nil {
		_ = awake.Process.Kill()
		_ = awake.Wait()
	}

	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		return 1, waitErr
	}
	code := cmd.ProcessState.ExitCode()
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		code = 128 + int(ws.Signal()) // the shell's convention
	}

	after, err := readCPU()
	if err != nil {
		return code, err
	}
	report(opts, cmd.ProcessState, wall, after.busySince(before), all, maxPressure)
	return code, nil
}

// waitSettled blocks until the CPU has been idle enough, at nominal thermal
// pressure, for opts.settle without a break.
func waitSettled(opts options, th *thermal, sigs <-chan os.Signal) error {
	logf("waiting for %s of at least %g%% idle CPU at nominal thermal pressure", opts.settle, opts.idle)
	prev, err := readCPU()
	if err != nil {
		return err
	}
	start := time.Now()
	prevAt, lastLog := start, start
	var quietSince time.Time

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case s := <-sigs:
			return fmt.Errorf("interrupted by %s", s)
		case now := <-tick.C:
			cur, err := readCPU()
			if err != nil {
				return err
			}
			idle := 100 * (1 - cur.busySince(prev))
			p := th.level()

			if idle >= opts.idle && p == 0 {
				// The sample covers the time since the previous one, so the
				// quiet stretch started then.
				if quietSince.IsZero() {
					quietSince = prevAt
				}
				if now.Sub(quietSince) >= opts.settle {
					logf("settled after %s", now.Sub(start).Round(time.Second))
					return nil
				}
			} else {
				quietSince = time.Time{}
			}
			prev, prevAt = cur, now

			if opts.timeout > 0 && now.Sub(start) >= opts.timeout {
				logf("busiest processes:\n%s", busiestProcesses())
				return fmt.Errorf("not settled after %s: idle %.1f%%, thermal pressure %s", opts.timeout, idle, p)
			}
			if now.Sub(lastLog) >= 5*time.Second {
				logf("waiting: idle %.1f%%, thermal pressure %s", idle, p)
				lastLog = now
			}
		}
	}
}

// report says how quiet the machine stayed during the run. The busy fraction
// covers every process, so the benchmark's own CPU time is taken out of it to
// leave what everything else used.
func report(opts options, ps *os.ProcessState, wall time.Duration, busy float64, all int, peak pressure) {
	own := (ps.UserTime() + ps.SystemTime()).Seconds() / wall.Seconds()
	others := busy*float64(all) - own
	if others < 0 {
		others = 0
	}
	allowed := (100 - opts.idle) / 100 * float64(all)

	logf("finished in %s, %s", wall.Round(time.Millisecond), ps)
	logf("thermal pressure peaked at %s, other processes used %.2f cores on average", peak, others)
	if peak != 0 {
		logf("warning: thermal pressure rose above nominal, the CPU was likely throttled")
	}
	if others > allowed {
		logf("warning: other processes used more than the %.2f cores allowed at the start", allowed)
	}
}

func status() error {
	ac, err := onAC()
	if err != nil {
		return err
	}
	th, err := openThermal()
	if err != nil {
		return err
	}
	defer th.close()
	perf, all, err := cores()
	if err != nil {
		return err
	}
	before, err := readCPU()
	if err != nil {
		return err
	}
	time.Sleep(time.Second)
	after, err := readCPU()
	if err != nil {
		return err
	}

	power := "battery"
	if ac {
		power = "AC"
	}
	lowPower := "unsupported"
	if on, err := lowPowerMode(); err == nil {
		lowPower = map[bool]string{true: "on", false: "off"}[on]
	}
	fmt.Printf("power             %s\n", power)
	fmt.Printf("thermal pressure  %s\n", th.level())
	fmt.Printf("cpu idle          %.1f%%\n", 100*(1-after.busySince(before)))
	fmt.Printf("low power mode    %s\n", lowPower)
	fmt.Printf("cores             %d performance of %d\n", perf, all)
	return nil
}

func busiestProcesses() string {
	out, err := exec.Command("ps", "-A", "-r", "-o", "pcpu=,comm=").Output()
	if err != nil {
		return err.Error()
	}
	lines := strings.SplitN(string(out), "\n", 6)
	return strings.Join(lines[:min(5, len(lines))], "\n")
}

// logf writes to stderr. The prefix is bracketed so benchstat doesn't read the
// line as a configuration line if stderr ends up in the results.
func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[quietbench] "+format+"\n", args...)
}
