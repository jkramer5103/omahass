package main

import (
	"bufio"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// System sensors read straight from /proc and /sys so sampling costs a few
// file reads instead of spawning processes. Only the slow loop (Wi-Fi name,
// microphone) shells out.

type cpuSample struct{ idle, total uint64 }

type netSample struct {
	iface   string
	rx, tx  uint64
	at      time.Time
	present bool
}

type Sensors struct {
	pc      *PC
	cpuPrev cpuSample
	netPrev netSample
	hasBat  string // e.g. /sys/class/power_supply/BAT0
	acPath  string
	homeSep bool
	tempIn  string
	fanIn   string
}

func NewSensors(pc *PC) *Sensors {
	s := &Sensors{pc: pc}
	s.probe()
	s.define()
	return s
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readInt(path string) (int64, bool) {
	v, err := strconv.ParseInt(readTrim(path), 10, 64)
	return v, err == nil
}

func (s *Sensors) probe() {
	supplies, _ := filepath.Glob("/sys/class/power_supply/*")
	for _, p := range supplies {
		switch readTrim(p + "/type") {
		case "Battery":
			if s.hasBat == "" && readTrim(p+"/scope") != "Device" {
				s.hasBat = p
			}
		case "Mains":
			if s.acPath == "" {
				s.acPath = p
			}
		}
	}

	var root, home syscall.Stat_t
	if syscall.Stat("/", &root) == nil && syscall.Stat("/home", &home) == nil {
		s.homeSep = root.Dev != home.Dev
	}

	// Prefer the CPU package sensor; fall back to whatever reports a
	// temperature so laptops without coretemp/k10temp still get one.
	hwmons, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	pref := map[string]int{"coretemp": 0, "k10temp": 0, "zenpower": 0, "cpu_thermal": 1, "thinkpad": 2, "acpitz": 3}
	best := 99
	for _, h := range hwmons {
		name := readTrim(h + "/name")
		if rank, ok := pref[name]; ok && rank < best {
			if _, err := os.Stat(h + "/temp1_input"); err == nil {
				best = rank
				s.tempIn = h + "/temp1_input"
			}
		}
		if s.fanIn == "" {
			if _, err := os.Stat(h + "/fan1_input"); err == nil {
				s.fanIn = h + "/fan1_input"
			}
		}
	}
}

func (s *Sensors) define() {
	d := func(e PCEntity) { s.pc.Define(e, nil) }
	d(PCEntity{Key: "cpu_usage", Platform: "sensor", Name: "CPU usage", Group: "System", Icon: "mdi:cpu-64-bit", Unit: "%", StateClass: "measurement", Precision: prec(0), DefaultOn: true})
	if s.tempIn != "" {
		d(PCEntity{Key: "cpu_temp", Platform: "sensor", Name: "CPU temperature", Group: "System", DeviceClass: "temperature", Unit: "°C", StateClass: "measurement", Precision: prec(0), DefaultOn: true})
	}
	if s.fanIn != "" {
		d(PCEntity{Key: "fan_speed", Platform: "sensor", Name: "Fan speed", Group: "System", Icon: "mdi:fan", Unit: "rpm", StateClass: "measurement", Precision: prec(0)})
	}
	d(PCEntity{Key: "load_1m", Platform: "sensor", Name: "Load (1m)", Group: "System", Icon: "mdi:gauge", StateClass: "measurement", Precision: prec(2)})
	d(PCEntity{Key: "memory_usage", Platform: "sensor", Name: "Memory usage", Group: "System", Icon: "mdi:memory", Unit: "%", StateClass: "measurement", Precision: prec(0), DefaultOn: true})
	d(PCEntity{Key: "memory_used", Platform: "sensor", Name: "Memory used", Group: "System", DeviceClass: "data_size", Unit: "GiB", StateClass: "measurement", Precision: prec(1)})
	d(PCEntity{Key: "swap_usage", Platform: "sensor", Name: "Swap usage", Group: "System", Icon: "mdi:swap-horizontal", Unit: "%", StateClass: "measurement", Precision: prec(0)})
	d(PCEntity{Key: "disk_root", Platform: "sensor", Name: "Disk usage (/)", Group: "System", Icon: "mdi:harddisk", Unit: "%", StateClass: "measurement", Precision: prec(0), DefaultOn: true})
	if s.homeSep {
		d(PCEntity{Key: "disk_home", Platform: "sensor", Name: "Disk usage (/home)", Group: "System", Icon: "mdi:harddisk", Unit: "%", StateClass: "measurement", Precision: prec(0), DefaultOn: true})
	}
	d(PCEntity{Key: "boot_time", Platform: "sensor", Name: "Last boot", Group: "System", DeviceClass: "timestamp", Category: "diagnostic", DefaultOn: true})
	d(PCEntity{Key: "kernel", Platform: "sensor", Name: "Kernel", Group: "System", Icon: "mdi:linux", Category: "diagnostic"})
	d(PCEntity{Key: "os", Platform: "sensor", Name: "Operating system", Group: "System", Icon: "mdi:information-outline", Category: "diagnostic"})

	if s.hasBat != "" {
		d(PCEntity{Key: "battery_level", Platform: "sensor", Name: "Battery", Group: "Power", DeviceClass: "battery", Unit: "%", StateClass: "measurement", Precision: prec(0), DefaultOn: true})
		d(PCEntity{Key: "battery_charging", Platform: "binary_sensor", Name: "Battery charging", Group: "Power", DeviceClass: "battery_charging", DefaultOn: true})
		d(PCEntity{Key: "battery_power", Platform: "sensor", Name: "Battery power draw", Group: "Power", DeviceClass: "power", Unit: "W", StateClass: "measurement", Precision: prec(1)})
	}
	if s.acPath != "" {
		d(PCEntity{Key: "ac_power", Platform: "binary_sensor", Name: "Plugged in", Group: "Power", DeviceClass: "plug", DefaultOn: true})
	}

	d(PCEntity{Key: "net_down", Platform: "sensor", Name: "Download speed", Group: "Network", DeviceClass: "data_rate", Unit: "kB/s", StateClass: "measurement", Precision: prec(0)})
	d(PCEntity{Key: "net_up", Platform: "sensor", Name: "Upload speed", Group: "Network", DeviceClass: "data_rate", Unit: "kB/s", StateClass: "measurement", Precision: prec(0)})
	d(PCEntity{Key: "ip_address", Platform: "sensor", Name: "IP address", Group: "Network", Icon: "mdi:ip-network", Category: "diagnostic", DefaultOn: true})
	d(PCEntity{Key: "wifi_ssid", Platform: "sensor", Name: "Wi-Fi network", Group: "Network", Icon: "mdi:wifi"})

	d(PCEntity{Key: "webcam_in_use", Platform: "binary_sensor", Name: "Webcam in use", Group: "Privacy", Icon: "mdi:webcam", DefaultOn: true})
	d(PCEntity{Key: "mic_in_use", Platform: "binary_sensor", Name: "Microphone in use", Group: "Privacy", Icon: "mdi:microphone", DefaultOn: true})
}

func (s *Sensors) Run(ctx context.Context, interval func() time.Duration) {
	s.static()
	s.slow()
	s.fast()
	fast := time.NewTimer(interval())
	slow := time.NewTicker(60 * time.Second)
	defer slow.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-fast.C:
			s.fast()
			fast.Reset(interval())
		case <-slow.C:
			s.slow()
		}
	}
}

func (s *Sensors) static() {
	v := map[string]any{"kernel": readTrim("/proc/sys/kernel/osrelease")}
	if f, err := os.Open("/etc/os-release"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if after, ok := strings.CutPrefix(sc.Text(), "PRETTY_NAME="); ok {
				v["os"] = strings.Trim(after, `"`)
			}
		}
		f.Close()
	}
	if f, err := os.Open("/proc/stat"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if after, ok := strings.CutPrefix(sc.Text(), "btime "); ok {
				if secs, err := strconv.ParseInt(strings.TrimSpace(after), 10, 64); err == nil {
					v["boot_time"] = time.Unix(secs, 0).UTC().Format(time.RFC3339)
				}
			}
		}
		f.Close()
	}
	s.pc.SetMany(v)
}

func (s *Sensors) fast() {
	v := map[string]any{}

	if cpu, ok := readCPU(); ok {
		if s.cpuPrev.total > 0 && cpu.total > s.cpuPrev.total {
			dt := float64(cpu.total - s.cpuPrev.total)
			di := float64(cpu.idle - s.cpuPrev.idle)
			v["cpu_usage"] = 100 * (1 - di/dt)
		}
		s.cpuPrev = cpu
	}
	if s.tempIn != "" {
		if milli, ok := readInt(s.tempIn); ok {
			v["cpu_temp"] = float64(milli) / 1000
		}
	}
	if s.fanIn != "" {
		if rpm, ok := readInt(s.fanIn); ok {
			v["fan_speed"] = float64(rpm)
		}
	}
	if fields := strings.Fields(readTrim("/proc/loadavg")); len(fields) > 0 {
		if l, err := strconv.ParseFloat(fields[0], 64); err == nil {
			v["load_1m"] = l
		}
	}

	mem := readMeminfo()
	if total := mem["MemTotal"]; total > 0 {
		used := total - mem["MemAvailable"]
		v["memory_usage"] = 100 * float64(used) / float64(total)
		v["memory_used"] = float64(used) / (1024 * 1024) // kB -> GiB
	}
	if st := mem["SwapTotal"]; st > 0 {
		v["swap_usage"] = 100 * float64(st-mem["SwapFree"]) / float64(st)
	} else {
		v["swap_usage"] = 0.0
	}

	if s.hasBat != "" {
		if c, ok := readInt(s.hasBat + "/capacity"); ok {
			v["battery_level"] = float64(c)
		}
		status := readTrim(s.hasBat + "/status")
		v["battery_charging"] = status == "Charging"
		if p, ok := readInt(s.hasBat + "/power_now"); ok {
			v["battery_power"] = float64(p) / 1e6
		} else if cur, ok := readInt(s.hasBat + "/current_now"); ok {
			if volt, ok := readInt(s.hasBat + "/voltage_now"); ok {
				v["battery_power"] = float64(cur) * float64(volt) / 1e12
			}
		}
	}
	if s.acPath != "" {
		v["ac_power"] = readTrim(s.acPath+"/online") == "1"
	}

	if ns, ok := readNet(); ok {
		if s.netPrev.present && s.netPrev.iface == ns.iface {
			secs := ns.at.Sub(s.netPrev.at).Seconds()
			if secs > 0 && ns.rx >= s.netPrev.rx && ns.tx >= s.netPrev.tx {
				v["net_down"] = float64(ns.rx-s.netPrev.rx) / 1000 / secs
				v["net_up"] = float64(ns.tx-s.netPrev.tx) / 1000 / secs
			}
		}
		s.netPrev = ns
	}

	v["webcam_in_use"] = webcamInUse()
	v["mic_in_use"] = micInUse()
	s.pc.SetMany(v)
}

func (s *Sensors) slow() {
	v := map[string]any{
		"disk_root":  diskUsage("/"),
		"ip_address": localIP(),
		"wifi_ssid":  wifiSSID(),
	}
	if s.homeSep {
		v["disk_home"] = diskUsage("/home")
	}
	s.pc.SetMany(v)
}

func readCPU() (cpuSample, bool) {
	line := ""
	if f, err := os.Open("/proc/stat"); err == nil {
		sc := bufio.NewScanner(f)
		if sc.Scan() {
			line = sc.Text()
		}
		f.Close()
	}
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuSample{}, false
	}
	var s cpuSample
	for i, f := range fields[1:] {
		n, _ := strconv.ParseUint(f, 10, 64)
		s.total += n
		if i == 3 || i == 4 { // idle + iowait
			s.idle += n
		}
	}
	return s, true
}

func readMeminfo() map[string]int64 {
	out := map[string]int64{}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) > 0 {
			n, _ := strconv.ParseInt(fields[0], 10, 64)
			out[k] = n
		}
	}
	return out
}

func defaultIface() string {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) > 2 && fields[1] == "00000000" {
			return fields[0]
		}
	}
	return ""
}

func readNet() (netSample, bool) {
	iface := defaultIface()
	if iface == "" {
		return netSample{}, false
	}
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return netSample{}, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name, rest, ok := strings.Cut(strings.TrimSpace(sc.Text()), ":")
		if !ok || name != iface {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 9 {
			break
		}
		rx, _ := strconv.ParseUint(fields[0], 10, 64)
		tx, _ := strconv.ParseUint(fields[8], 10, 64)
		return netSample{iface: iface, rx: rx, tx: tx, at: time.Now(), present: true}, true
	}
	return netSample{}, false
}

func diskUsage(path string) any {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil || st.Blocks == 0 {
		return nil
	}
	// Match df: used / (used + available to unprivileged users).
	used := float64(st.Blocks-st.Bfree) * float64(st.Bsize)
	avail := float64(st.Bavail) * float64(st.Bsize)
	return 100 * used / (used + avail)
}

func localIP() any {
	// UDP "connect" picks the outbound source address without sending a
	// packet.
	c, err := net.Dial("udp", "1.1.1.1:80")
	if err != nil {
		return nil
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}

func runOut(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

func wifiSSID() any {
	out, err := runOut(3*time.Second, "nmcli", "-t", "-f", "active,ssid", "dev", "wifi", "list", "--rescan", "no")
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(out, "\n") {
		if after, ok := strings.CutPrefix(line, "yes:"); ok {
			return strings.ReplaceAll(after, `\:`, ":")
		}
	}
	return nil
}

// webcamInUse looks for any process holding a /dev/video* node open. Only
// processes we can read (our own) are visible, which covers browsers and
// video-call apps.
func webcamInUse() bool {
	procs, _ := os.ReadDir("/proc")
	self := strconv.Itoa(os.Getpid())
	for _, p := range procs {
		name := p.Name()
		if name[0] < '0' || name[0] > '9' || name == self {
			continue
		}
		fds, err := os.ReadDir("/proc/" + name + "/fd")
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink("/proc/" + name + "/fd/" + fd.Name())
			if err == nil && strings.HasPrefix(target, "/dev/video") {
				return true
			}
		}
	}
	return false
}

// micInUse counts PipeWire/Pulse recording streams that read from a real
// input (not a .monitor of an output, which is what visualizers use).
func micInUse() bool {
	sources, err := runOut(2*time.Second, "pactl", "list", "short", "sources")
	if err != nil {
		return false
	}
	monitor := map[string]bool{}
	for _, line := range strings.Split(sources, "\n") {
		f := strings.Split(line, "\t")
		if len(f) >= 2 && strings.HasSuffix(f[1], ".monitor") {
			monitor[f[0]] = true
		}
	}
	outputs, err := runOut(2*time.Second, "pactl", "list", "short", "source-outputs")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(outputs, "\n") {
		f := strings.Split(line, "\t")
		if len(f) >= 2 && !monitor[f[1]] {
			return true
		}
	}
	return false
}
