package collector

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// fakeDevice answers only the calls named in answers; everything else
// returns an error, as a driver does when its version no longer matches the
// loaded kernel module.
type fakeDevice struct{ answers map[string]bool }

func (f fakeDevice) ret(name string) nvml.Return {
	if f.answers[name] {
		return nvml.SUCCESS
	}
	return nvml.ERROR_UNKNOWN
}

func (f fakeDevice) GetUtilizationRates() (nvml.Utilization, nvml.Return) {
	return nvml.Utilization{Gpu: 40, Memory: 30}, f.ret("GetUtilizationRates")
}
func (f fakeDevice) GetMemoryInfo() (nvml.Memory, nvml.Return) {
	return nvml.Memory{Used: 2048 * 1024 * 1024}, f.ret("GetMemoryInfo")
}
func (f fakeDevice) GetTemperature(nvml.TemperatureSensors) (uint32, nvml.Return) {
	return 71, f.ret("GetTemperature")
}
func (f fakeDevice) GetFanSpeed() (uint32, nvml.Return) { return 46, f.ret("GetFanSpeed") }
func (f fakeDevice) GetPowerUsage() (uint32, nvml.Return) {
	return 532000, f.ret("GetPowerUsage")
}
func (f fakeDevice) GetEnforcedPowerLimit() (uint32, nvml.Return) {
	return 600000, f.ret("GetEnforcedPowerLimit")
}
func (f fakeDevice) GetClockInfo(nvml.ClockType) (uint32, nvml.Return) {
	return 2685, f.ret("GetClockInfo")
}
func (f fakeDevice) GetPcieThroughput(nvml.PcieUtilCounter) (uint32, nvml.Return) {
	return 10343, f.ret("GetPcieThroughput")
}
func (f fakeDevice) GetPerformanceState() (nvml.Pstates, nvml.Return) {
	return nvml.PSTATE_1, f.ret("GetPerformanceState")
}
func (f fakeDevice) GetEncoderUtilization() (uint32, uint32, nvml.Return) {
	return 0, 0, f.ret("GetEncoderUtilization")
}
func (f fakeDevice) GetDecoderUtilization() (uint32, uint32, nvml.Return) {
	return 0, 0, f.ret("GetDecoderUtilization")
}

// A driver that answers with errors instead of blocking would otherwise
// yield a full row of zeros every second: indistinguishable from an idle
// GPU on the dashboard, and fresh enough to keep the staleness checks quiet
// while nothing is being measured.
func TestCollectDeviceRejectsADeviceThatAnswersNothing(t *testing.T) {
	_, ok := collectDevice(fakeDevice{}, 0, 1789570257)
	if ok {
		t.Error("a device that failed every call was reported as a sample")
	}
}

// A single answer is enough: fan speed and encoder counters are legitimately
// unsupported on some cards, and dropping those devices would lose real data.
func TestCollectDeviceKeepsPartialAnswers(t *testing.T) {
	dev := fakeDevice{answers: map[string]bool{"GetTemperature": true}}

	m, ok := collectDevice(dev, 3, 1789570257)
	if !ok {
		t.Fatal("a device that answered one call was dropped")
	}
	if m.Temperature != 71 {
		t.Errorf("temperature: got %d, want 71", m.Temperature)
	}
	if m.GPUID != 3 {
		t.Errorf("gpu id: got %d, want 3", m.GPUID)
	}
	if m.PowerDraw != 0 {
		t.Errorf("power draw came from a failed call: got %v", m.PowerDraw)
	}
}

func TestCollectDeviceReadsEveryAnswer(t *testing.T) {
	answers := map[string]bool{}
	for _, name := range []string{
		"GetUtilizationRates", "GetMemoryInfo", "GetTemperature", "GetFanSpeed",
		"GetPowerUsage", "GetEnforcedPowerLimit", "GetClockInfo",
		"GetPcieThroughput", "GetPerformanceState", "GetEncoderUtilization",
		"GetDecoderUtilization",
	} {
		answers[name] = true
	}

	m, ok := collectDevice(fakeDevice{answers: answers}, 0, 1789570257)
	if !ok {
		t.Fatal("a fully answering device was dropped")
	}
	if m.GPUUtil != 40 || m.MemUsed != 2048 || m.Temperature != 71 ||
		m.FanSpeed != 46 || m.PowerDraw != 532 || m.PowerLimit != 600 ||
		m.ClockGfx != 2685 || m.PCIeTx != 10343 || m.PState != 1 {
		t.Errorf("fields not read as expected: %+v", m)
	}
}
