package mapper_test

import (
	"strings"
	"testing"

	"github.com/guneshm03/planprice/internal/mapper"
)

// Unit prices below are from the embedded snapshot (us-east-1 unless noted):
// t3.medium Linux $0.0416/h, Windows $0.06/h; m6i.large $0.096/h;
// eu-west-1 t3.large $0.0912/h; t3 CPU credits $0.05/vCPU-h;
// EBS gp2 $0.10, gp3 $0.08, st1 $0.045, sc1 $0.015, standard $0.05,
// io1/io2 $0.125 per GB-month; gp3 IOPS $0.005, io1 $0.065, io2 tiers
// $0.065 / $0.0455 / $0.03185 per IOPS-month; gp3 throughput $40.96 per
// GiBps-month = $0.04 per MiBps-month.

func TestInstanceLinuxWithRootVolume(t *testing.T) {
	rc := price(t, res{typ: "aws_instance", attrs: map[string]any{
		"instance_type":     "t3.medium",
		"root_block_device": []any{map[string]any{"volume_type": "gp3", "volume_size": 30}},
	}})
	costIs(t, rc, "instance", "30.368")                       // 730 h * 0.0416
	costIs(t, rc, "root_block_device/storage", "2.4")         // 30 GB * 0.08
	statusIs(t, rc, "cpu_credits", mapper.StatusUsageMissing) // t3 defaults to unlimited
	totalIs(t, rc, "32.768")
}

func TestInstanceWindowsPartTimeAndCPUCredits(t *testing.T) {
	rc := price(t, res{typ: "aws_instance",
		attrs: map[string]any{"instance_type": "t3.medium", "root_block_device": []any{map[string]any{"volume_type": "gp2", "volume_size": 50}}},
		usage: "operating_system: windows\nmonthly_hours: 200\nmonthly_cpu_credit_hours: 100"})
	costIs(t, rc, "instance", "12")                 // 200 h * 0.06
	costIs(t, rc, "cpu_credits", "9.6")             // 100 vCPU-h * 0.096 (Windows rate)
	costIs(t, rc, "root_block_device/storage", "5") // 50 GB * 0.10
	if !strings.Contains(component(t, rc, "instance").Name, "Windows") {
		t.Error("name should say Windows")
	}
}

func TestInstanceCreditModes(t *testing.T) {
	linux := price(t, res{typ: "aws_instance", attrs: map[string]any{"instance_type": "t3.large",
		"credit_specification": []any{map[string]any{"cpu_credits": "unlimited"}}},
		usage: "monthly_cpu_credit_hours: 100"})
	costIs(t, linux, "cpu_credits", "5") // 100 * 0.05

	standard := price(t, res{typ: "aws_instance", attrs: map[string]any{"instance_type": "t3.large",
		"credit_specification": []any{map[string]any{"cpu_credits": "standard"}}}})
	if hasComponent(standard, "cpu_credits") {
		t.Error("standard credit mode should have no credit component")
	}
	t2 := price(t, res{typ: "aws_instance", attrs: map[string]any{"instance_type": "t2.micro"}})
	if hasComponent(t2, "cpu_credits") {
		t.Error("t2 defaults to standard credits")
	}
	m6 := price(t, res{typ: "aws_instance", attrs: map[string]any{"instance_type": "m6i.large"}})
	if hasComponent(m6, "cpu_credits") {
		t.Error("non-burstable instance has a credit component")
	}
}

func TestInstanceDefaultRootAndAttachedVolumes(t *testing.T) {
	rc := price(t, res{typ: "aws_instance", attrs: map[string]any{
		"instance_type": "m6i.large",
		"ebs_block_device": []any{
			map[string]any{"device_name": "/dev/sdf", "volume_type": "st1", "volume_size": 500},
			map[string]any{"device_name": "/dev/sdg", "volume_type": "io1", "volume_size": 100, "iops": 1000},
		},
	}})
	costIs(t, rc, "instance", "70.08")                         // 730 * 0.096
	costIs(t, rc, "root_block_device/storage", "0.8")          // assumed 8 GB gp2 * 0.10
	costIs(t, rc, "ebs_block_device:/dev/sdf/storage", "22.5") // 500 * 0.045
	costIs(t, rc, "ebs_block_device:/dev/sdg/storage", "12.5") // 100 * 0.125
	costIs(t, rc, "ebs_block_device:/dev/sdg/iops", "65")      // 1000 * 0.065
	totalIs(t, rc, "170.88")
	if len(rc.Notes) != 1 || !strings.Contains(rc.Notes[0], "8 GB gp2") {
		t.Errorf("notes: %v", rc.Notes)
	}
}

func TestInstanceOtherRegion(t *testing.T) {
	rc := price(t, res{typ: "aws_instance", region: "eu-west-1", attrs: map[string]any{"instance_type": "t3.large",
		"root_block_device": []any{map[string]any{"volume_type": "gp3", "volume_size": 40}}}})
	costIs(t, rc, "instance", "66.576")                // 730 * 0.0912
	costIs(t, rc, "root_block_device/storage", "3.52") // 40 * 0.088 (eu-west-1 gp3)
}

func TestInstanceUnpriceableStates(t *testing.T) {
	unknown := price(t, res{typ: "aws_instance", attrs: map[string]any{}, unknown: map[string]any{"instance_type": true}})
	statusIs(t, unknown, "instance", mapper.StatusUnknown)
	dedicated := price(t, res{typ: "aws_instance", attrs: map[string]any{"instance_type": "m6i.large", "tenancy": "dedicated"}})
	statusIs(t, dedicated, "instance", mapper.StatusUnsupported)
	missing := price(t, res{typ: "aws_instance", attrs: map[string]any{"instance_type": "x99.huge"}})
	statusIs(t, missing, "instance", mapper.StatusPriceNotFound)
	rootUnknown := price(t, res{typ: "aws_instance", attrs: map[string]any{"instance_type": "m6i.large",
		"root_block_device": []any{map[string]any{"volume_type": "gp3"}}},
		unknown: map[string]any{"root_block_device": []any{map[string]any{"volume_size": true}}}})
	costIs(t, rootUnknown, "root_block_device/storage", "0.64") // falls back to 8 GB * 0.08
}

func TestEBSVolumeTypes(t *testing.T) {
	cases := []struct {
		name  string
		attrs map[string]any
		want  map[string]string
		total string
	}{
		{"gp2", map[string]any{"type": "gp2", "size": 200}, map[string]string{"storage": "20"}, "20"},
		{"default type is gp2", map[string]any{"size": 10}, map[string]string{"storage": "1"}, "1"},
		{"gp3 baseline", map[string]any{"type": "gp3", "size": 100, "iops": 3000, "throughput": 125}, map[string]string{"storage": "8"}, "8"},
		// 200*0.08 + (4000-3000)*0.005 + (250-125)*0.04
		{"gp3 above baseline", map[string]any{"type": "gp3", "size": 200, "iops": 4000, "throughput": 250},
			map[string]string{"storage": "16", "iops": "5", "throughput": "5"}, "26"},
		{"st1", map[string]any{"type": "st1", "size": 500}, map[string]string{"storage": "22.5"}, "22.5"},
		{"sc1", map[string]any{"type": "sc1", "size": 1000}, map[string]string{"storage": "15"}, "15"},
		{"standard", map[string]any{"type": "standard", "size": 100}, map[string]string{"storage": "5"}, "5"},
		{"io1", map[string]any{"type": "io1", "size": 100, "iops": 1000}, map[string]string{"storage": "12.5", "iops": "65"}, "77.5"},
		// 32,000*0.065 + 8,000*0.0455
		{"io2 two tiers", map[string]any{"type": "io2", "size": 500, "iops": 40000},
			map[string]string{"storage": "62.5", "iops_tier1": "2080", "iops_tier2": "364"}, "2506.5"},
		// 32,000*0.065 + 32,000*0.0455 + 6,000*0.03185
		{"io2 three tiers", map[string]any{"type": "io2", "size": 500, "iops": 70000},
			map[string]string{"storage": "62.5", "iops_tier1": "2080", "iops_tier2": "1456", "iops_tier3": "191.1"}, "3789.6"},
		{"io2 one tier", map[string]any{"type": "io2", "size": 100, "iops": 1000}, map[string]string{"iops_tier1": "65"}, "77.5"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rc := price(t, res{typ: "aws_ebs_volume", attrs: c.attrs})
			for k, v := range c.want {
				costIs(t, rc, k, v)
			}
			totalIs(t, rc, c.total)
			if c.name == "io2 one tier" && hasComponent(rc, "iops_tier2") {
				t.Error("unused io2 tier listed")
			}
		})
	}
}

func TestEBSUnpriceableStates(t *testing.T) {
	statusIs(t, price(t, res{typ: "aws_ebs_volume", attrs: map[string]any{"type": "io1", "size": 100}}), "iops", mapper.StatusUnknown)
	statusIs(t, price(t, res{typ: "aws_ebs_volume", attrs: map[string]any{"type": "io2", "size": 100}, unknown: map[string]any{"iops": true}}), "iops", mapper.StatusUnknown)
	statusIs(t, price(t, res{typ: "aws_ebs_volume", attrs: map[string]any{"type": "gp9", "size": 100}}), "storage", mapper.StatusUnsupported)
	statusIs(t, price(t, res{typ: "aws_ebs_volume", attrs: map[string]any{"type": "gp3"}, unknown: map[string]any{"size": true}}), "storage", mapper.StatusUnknown)
	statusIs(t, price(t, res{typ: "aws_ebs_volume", attrs: map[string]any{"type": "gp3", "snapshot_id": "snap-1"}}), "storage", mapper.StatusUnknown)
	statusIs(t, price(t, res{typ: "aws_ebs_volume", attrs: map[string]any{"type": "gp3"}}), "storage", mapper.StatusUnknown)
}
