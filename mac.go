package main

/*
#include <mach/mach.h>
#include <notify.h>
#include <libkern/OSThermalNotification.h>

// cpu_ticks reads the CPU time spent in each state, summed over every core,
// since boot.
static int cpu_ticks(unsigned int ticks[CPU_STATE_MAX]) {
	static mach_port_t host = MACH_PORT_NULL;
	if (host == MACH_PORT_NULL) {
		host = mach_host_self();
	}
	host_cpu_load_info_data_t info;
	mach_msg_type_number_t count = HOST_CPU_LOAD_INFO_COUNT;
	if (host_statistics(host, HOST_CPU_LOAD_INFO, (host_info_t)&info, &count) != KERN_SUCCESS) {
		return -1;
	}
	for (int i = 0; i < CPU_STATE_MAX; i++) {
		ticks[i] = info.cpu_ticks[i];
	}
	return 0;
}

static int thermal_register(int *token) {
	return notify_register_check(kOSThermalNotificationPressureLevelName, token) == NOTIFY_STATUS_OK ? 0 : -1;
}

static int thermal_level(int token, uint64_t *level) {
	return notify_get_state(token, level) == NOTIFY_STATUS_OK ? 0 : -1;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"syscall"
)

// cpuSample is the cumulative CPU time, in ticks, spent in each state across
// every core. The counters are 32 bits and wrap, so only differences between
// two samples mean anything.
type cpuSample struct {
	user, system, idle, nice uint32
}

func readCPU() (cpuSample, error) {
	var t [C.CPU_STATE_MAX]C.uint
	if C.cpu_ticks(&t[0]) != 0 {
		return cpuSample{}, errors.New("reading CPU load: host_statistics failed")
	}
	return cpuSample{
		user:   uint32(t[C.CPU_STATE_USER]),
		system: uint32(t[C.CPU_STATE_SYSTEM]),
		idle:   uint32(t[C.CPU_STATE_IDLE]),
		nice:   uint32(t[C.CPU_STATE_NICE]),
	}, nil
}

// busySince returns the fraction of CPU time, from 0 to 1 across all cores,
// that was not idle between prev and s.
func (s cpuSample) busySince(prev cpuSample) float64 {
	busy := uint64(s.user-prev.user) + uint64(s.system-prev.system) + uint64(s.nice-prev.nice)
	total := busy + uint64(s.idle-prev.idle)
	if total == 0 {
		return 0
	}
	return float64(busy) / float64(total)
}

// pressure is the macOS thermal pressure level. Anything above nominal means
// the system is limiting performance to shed heat.
type pressure int

const pressureUnknown pressure = -1

func (p pressure) String() string {
	switch p {
	case 0:
		return "nominal"
	case 1:
		return "moderate"
	case 2:
		return "heavy"
	case 3:
		return "trapping"
	case 4:
		return "sleeping"
	default:
		return "unknown"
	}
}

type thermal struct {
	token C.int
}

func openThermal() (*thermal, error) {
	var t thermal
	if C.thermal_register(&t.token) != 0 {
		return nil, errors.New("subscribing to thermal pressure notifications failed")
	}
	return &t, nil
}

func (t *thermal) level() pressure {
	var level C.uint64_t
	if C.thermal_level(t.token, &level) != 0 {
		return pressureUnknown
	}
	return pressure(level)
}

func (t *thermal) close() {
	C.notify_cancel(t.token)
}

// cores returns the number of performance and of all logical cores. perf is 0
// on Macs without performance levels, like Intel ones.
func cores() (perf, all int, err error) {
	n, err := syscall.SysctlUint32("hw.logicalcpu")
	if err != nil {
		return 0, 0, fmt.Errorf("reading hw.logicalcpu: %w", err)
	}
	p, err := syscall.SysctlUint32("hw.perflevel0.logicalcpu")
	if err != nil {
		return 0, int(n), nil
	}
	return int(p), int(n), nil
}
