package mapper_test

import (
	"testing"

	"github.com/guneshm03/planprice/internal/mapper"
)

// RDS prices (us-east-1 unless noted): PostgreSQL db.t3.medium Single-AZ
// $0.072/h; PostgreSQL db.m6g.large Multi-AZ $0.318/h; eu-west-1 MySQL
// db.r6g.large Single-AZ $0.237/h. Storage per GB-month: gp2/gp3 Single-AZ
// $0.115, Multi-AZ $0.23; io1/io2 $0.125 (eu-west-1 io1 $0.138). IOPS-month:
// gp3 extra $0.02 Single / $0.04 Multi; io1 $0.10 (eu-west-1 $0.11); io2
// $0.10. gp3 throughput $0.08 per MiBps-month. Backup $0.095 per GB-month.

func TestRDSPostgresSingleAZgp3(t *testing.T) {
	rc := price(t, res{typ: "aws_db_instance", attrs: map[string]any{
		"engine": "postgres", "instance_class": "db.t3.medium", "multi_az": false,
		"storage_type": "gp3", "allocated_storage": 100, "iops": 3000,
	}})
	costIs(t, rc, "instance", "52.56") // 730 * 0.072
	costIs(t, rc, "storage", "11.5")   // 100 * 0.115
	if hasComponent(rc, "iops") {
		t.Error("3,000 IOPS is within the gp3 baseline")
	}
	totalIs(t, rc, "64.06")
}

func TestRDSMultiAZgp3AboveLargeVolumeBaseline(t *testing.T) {
	rc := price(t, res{typ: "aws_db_instance", attrs: map[string]any{
		"engine": "postgres", "instance_class": "db.m6g.large", "multi_az": true,
		"storage_type": "gp3", "allocated_storage": 500, "iops": 15000,
	}})
	costIs(t, rc, "instance", "232.14") // 730 * 0.318
	costIs(t, rc, "storage", "115")     // 500 * 0.23
	costIs(t, rc, "iops", "120")        // (15,000 - 12,000) * 0.04
	totalIs(t, rc, "467.14")
}

func TestRDSgp3Throughput(t *testing.T) {
	rc := price(t, res{typ: "aws_db_instance", attrs: map[string]any{
		"engine": "mysql", "instance_class": "db.t3.medium",
		"storage_type": "gp3", "allocated_storage": 500, "iops": 12000, "storage_throughput": 750,
	}})
	costIs(t, rc, "storage", "57.5")  // 500 * 0.115
	costIs(t, rc, "throughput", "20") // (750 - 500) * 0.08
	small := price(t, res{typ: "aws_db_instance", attrs: map[string]any{
		"engine": "mysql", "instance_class": "db.t3.medium",
		"storage_type": "gp3", "allocated_storage": 100, "storage_throughput": 200,
	}})
	costIs(t, small, "throughput", "6") // (200 - 125) * 0.08, small-volume baseline
}

func TestRDSio1OtherRegion(t *testing.T) {
	rc := price(t, res{typ: "aws_db_instance", region: "eu-west-1", attrs: map[string]any{
		"engine": "mysql", "instance_class": "db.r6g.large",
		"storage_type": "io1", "allocated_storage": 200, "iops": 3000,
	}})
	costIs(t, rc, "instance", "173.01") // 730 * 0.237
	costIs(t, rc, "storage", "27.6")    // 200 * 0.138
	costIs(t, rc, "iops", "330")        // 3,000 * 0.11
	totalIs(t, rc, "530.61")
}

func TestRDSStorageDefaultsAndBackup(t *testing.T) {
	io1 := price(t, res{typ: "aws_db_instance", attrs: map[string]any{
		"engine": "mariadb", "instance_class": "db.t3.medium", "allocated_storage": 100, "iops": 1000,
	}})
	costIs(t, io1, "storage", "12.5") // unset type with iops -> io1, 100 * 0.125
	costIs(t, io1, "iops", "100")     // 1,000 * 0.10
	gp2 := price(t, res{typ: "aws_db_instance", attrs: map[string]any{
		"engine": "postgres", "instance_class": "db.t3.medium", "allocated_storage": 20,
	}, usage: "additional_backup_storage_gb: 150"})
	costIs(t, gp2, "storage", "2.3")  // unset type -> gp2, 20 * 0.115
	costIs(t, gp2, "backup", "14.25") // 150 * 0.095
	io2 := price(t, res{typ: "aws_db_instance", attrs: map[string]any{
		"engine": "postgres", "instance_class": "db.t3.medium", "storage_type": "io2", "allocated_storage": 100, "iops": 3000,
	}})
	costIs(t, io2, "storage", "12.5") // 100 * 0.125
	costIs(t, io2, "iops", "300")     // 3,000 * 0.10
	magnetic := price(t, res{typ: "aws_db_instance", attrs: map[string]any{
		"engine": "postgres", "instance_class": "db.t3.medium", "storage_type": "standard", "allocated_storage": 100, "multi_az": true,
	}})
	costIs(t, magnetic, "storage", "20") // 100 * 0.20 (Multi-AZ magnetic)
}

func TestRDSUnpriceableStates(t *testing.T) {
	oracle := price(t, res{typ: "aws_db_instance", attrs: map[string]any{"engine": "oracle-ee", "instance_class": "db.m5.large", "allocated_storage": 100}})
	statusIs(t, oracle, "instance", mapper.StatusUnsupported)
	costIs(t, oracle, "storage", "11.5") // storage is engine-independent
	unknownClass := price(t, res{typ: "aws_db_instance", attrs: map[string]any{"engine": "postgres", "allocated_storage": 100}, unknown: map[string]any{"instance_class": true}})
	statusIs(t, unknownClass, "instance", mapper.StatusUnknown)
	noStorage := price(t, res{typ: "aws_db_instance", attrs: map[string]any{"engine": "postgres", "instance_class": "db.t3.medium"}})
	statusIs(t, noStorage, "storage", mapper.StatusUnknown)
	io1NoIOPS := price(t, res{typ: "aws_db_instance", attrs: map[string]any{"engine": "postgres", "instance_class": "db.t3.medium", "storage_type": "io1", "allocated_storage": 100}})
	statusIs(t, io1NoIOPS, "iops", mapper.StatusUnknown)
	weird := price(t, res{typ: "aws_db_instance", attrs: map[string]any{"engine": "postgres", "instance_class": "db.t3.medium", "storage_type": "aurora", "allocated_storage": 100}})
	statusIs(t, weird, "storage", mapper.StatusUnsupported)
}

// S3 prices (us-east-1): Standard $0.023 / $0.022 / $0.021 per GB-month
// (bands 0-50 TB, 50-500 TB, 500 TB+); Intelligent-Tiering frequent tier
// same as Standard; Standard-IA $0.0125; One Zone-IA $0.01; Glacier Instant
// Retrieval $0.004; Glacier Flexible Retrieval $0.0036. Requests: Standard
// tier 1 $0.005 and tier 2 $0.0004 per 1k; IA retrieval $0.01/GB.

func TestS3NoUsage(t *testing.T) {
	rc := price(t, res{typ: "aws_s3_bucket", attrs: map[string]any{"bucket": "b"}})
	statusIs(t, rc, "standard.storage", mapper.StatusUsageMissing)
	if c := component(t, rc, "standard.storage"); c.UnitPrice == nil || c.UnitPrice.String() != "0.023" {
		t.Errorf("usage-missing component should still show the rate: %+v", c.UnitPrice)
	}
	totalIs(t, rc, "0")
}

func TestS3StorageClassesAndRequests(t *testing.T) {
	rc := price(t, res{typ: "aws_s3_bucket", attrs: map[string]any{"bucket": "b"}, usage: `
storage_classes:
  standard: {storage_gb: 60000, monthly_tier_1_requests: 400000, monthly_tier_2_requests: 12000000}
  standard_infrequent_access: {storage_gb: 8000, monthly_retrieval_gb: 300, monthly_tier_2_requests: 1000}
  one_zone_infrequent_access: {storage_gb: 1000}
  glacier_instant_retrieval: {storage_gb: 20000}
  glacier_flexible_retrieval: {storage_gb: 1000}
  intelligent_tiering: {storage_gb: 100}`})
	costIs(t, rc, "standard/storage", "1371.2")                // 51,200*0.023 + 8,800*0.022
	costIs(t, rc, "standard/tier1", "2")                       // 400k * $0.005/1k
	costIs(t, rc, "standard/tier2", "4.8")                     // 12M * $0.0004/1k
	costIs(t, rc, "standard_infrequent_access/storage", "100") // 8,000 * 0.0125
	costIs(t, rc, "standard_infrequent_access/retrieval", "3") // 300 * 0.01
	costIs(t, rc, "standard_infrequent_access/tier2", "0.001") // 1k * $0.001/1k
	costIs(t, rc, "one_zone_infrequent_access/storage", "10")  // 1,000 * 0.01
	costIs(t, rc, "glacier_instant_retrieval/storage", "80")   // 20,000 * 0.004
	costIs(t, rc, "glacier_flexible_retrieval/storage", "3.6") // 1,000 * 0.0036
	costIs(t, rc, "intelligent_tiering/storage", "2.3")        // 100 * 0.023
	totalIs(t, rc, "1576.901")
	tier1 := component(t, rc, "standard/tier1")
	if tier1.Unit != "thousand requests" || tier1.Quantity.String() != "400" || tier1.UnitPrice.String() != "0.005" {
		t.Errorf("request display scaling: %s %s at %s", tier1.Quantity, tier1.Unit, tier1.UnitPrice)
	}
	if hasComponent(rc, "standard/retrieval") {
		t.Error("standard has no retrieval fee")
	}
}

func TestS3ClassWithoutStorageValue(t *testing.T) {
	rc := price(t, res{typ: "aws_s3_bucket", attrs: map[string]any{}, usage: "storage_classes:\n  standard: {monthly_tier_2_requests: 1000000}"})
	statusIs(t, rc, "standard/storage", mapper.StatusUsageMissing)
	costIs(t, rc, "standard/tier2", "0.4") // 1M * $0.0004/1k
}
