package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
)

func onAC() (bool, error) {
	out, err := exec.Command("pmset", "-g", "batt").Output()
	if err != nil {
		return false, err
	}
	return strings.Contains(string(out), "'AC Power'"), nil
}

func lowPowerMode() (bool, error) {
	out, err := exec.Command("pmset", "-g").Output()
	if err != nil {
		return false, err
	}
	return parseLowPowerMode(string(out))
}

func parseLowPowerMode(pmset string) (bool, error) {
	for _, line := range strings.Split(pmset, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "lowpowermode" {
			return f[1] == "1", nil
		}
	}
	return false, errors.New("this Mac does not report lowpowermode in 'pmset -g'")
}

// setLowPowerMode changes the setting for when on AC power, the only source
// quietbench runs on, and leaves the battery setting alone. sudo may prompt
// for a password.
func setLowPowerMode(on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	cmd := exec.Command("sudo", "pmset", "-c", "lowpowermode", v)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stderr, os.Stderr
	return cmd.Run()
}
