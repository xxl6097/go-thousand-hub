//go:build linux

package agent

import (
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/xxl6097/go-thousand-hub/internal/protocol"
)

func kernelInfo() string {
	var u syscall.Utsname
	if err := syscall.Uname(&u); err != nil {
		return ""
	}
	rel := u.Release
	n := 0
	for i, c := range rel {
		if c == 0 {
			n = i
			break
		}
	}
	b := make([]byte, n)
	for i := 0; i < n; i++ {
		b[i] = byte(rel[i])
	}
	return string(b)
}

// ---------- Linux /proc 指标采集 ----------

var (
	prevIdle, prevTotal atomic.Uint64
	firstCPU            atomic.Bool
)

func collect(hostname string) protocol.Metrics {
	m := protocol.Metrics{Hostname: hostname}
	m.UptimeSec = uptimeSec()
	l1, l5, l15, procs := loadInfo()
	m.Load1, m.Load5, m.Load15 = l1, l5, l15
	m.Procs = procs
	m.CPU = cpuPercent()
	t, a := memInfo()
	m.MemTotal, m.MemAvail = t, a
	m.MemUsed = t - a
	dt, du := diskUsage("/")
	m.DiskTotal, m.DiskUsed = dt, du
	return m
}

func uptimeSec() int64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	sec, _ := strconv.ParseFloat(f[0], 64)
	return int64(sec)
}

func cpuPercent() float64 {
	idle, total := readCPUStat()
	if !firstCPU.Swap(true) {
		prevIdle.Store(idle)
		prevTotal.Store(total)
		return 0
	}
	dIdle := idle - prevIdle.Load()
	dTotal := total - prevTotal.Load()
	prevIdle.Store(idle)
	prevTotal.Store(total)
	if dTotal == 0 {
		return 0
	}
	p := 100.0 * float64(dTotal-dIdle) / float64(dTotal)
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	return p
}

func readCPUStat() (idle, total uint64) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0
	}
	line := ""
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "cpu ") {
			line = l
			break
		}
	}
	if line == "" {
		return 0, 0
	}
	fields := strings.Fields(line)
	var vals []uint64
	for _, f := range fields[1:] {
		v, _ := strconv.ParseUint(f, 10, 64)
		vals = append(vals, v)
	}
	if len(vals) < 4 {
		return 0, 0
	}
	idle = vals[3]
	if len(vals) >= 5 { // iowait 计入 idle(与 top 口径一致)
		idle += vals[4]
	}
	for _, v := range vals {
		total += v
	}
	return idle, total
}

func memInfo() (total, avail uint64) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			total = kB(line)
		case strings.HasPrefix(line, "MemAvailable:"):
			avail = kB(line)
		}
	}
	return total, avail
}

func kB(line string) uint64 {
	f := strings.Fields(line)
	if len(f) < 2 {
		return 0
	}
	v, _ := strconv.ParseUint(f[1], 10, 64)
	return v * 1024
}

func diskUsage(path string) (total, used uint64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0
	}
	total = st.Blocks * uint64(st.Bsize)
	free := st.Bavail * uint64(st.Bsize)
	used = total - free
	return total, used
}

func loadInfo() (l1, l5, l15 float64, procs int) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return
	}
	f := strings.Fields(string(b))
	if len(f) >= 4 {
		l1, _ = strconv.ParseFloat(f[0], 64)
		l5, _ = strconv.ParseFloat(f[1], 64)
		l15, _ = strconv.ParseFloat(f[2], 64)
		procs, _ = strconv.Atoi(strings.Split(f[3], "/")[0])
	}
	return
}
