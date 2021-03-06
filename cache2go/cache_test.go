package cache2go

import "testing"

func TestRemKey(t *testing.T) {
	Cache("t11_mm", "test")
	if t1, err := GetCached("t11_mm"); err != nil || t1 != "test" {
		t.Error("Error setting cache: ", err, t1)
	}
	RemKey("t11_mm")
	if t1, err := GetCached("t11_mm"); err == nil || t1 == "test" {
		t.Error("Error removing cached key")
	}
}

func TestTransaction(t *testing.T) {
	BeginTransaction()
	Cache("t11_mm", "test")
	if t1, err := GetCached("t11_mm"); err == nil || t1 == "test" {
		t.Error("Error in transaction cache")
	}
	Cache("t12_mm", "test")
	RemKey("t11_mm")
	CommitTransaction()
	if t1, err := GetCached("t12_mm"); err != nil || t1 != "test" {
		t.Error("Error commiting transaction")
	}
	if t1, err := GetCached("t11_mm"); err == nil || t1 == "test" {
		t.Error("Error in transaction cache")
	}
}

func TestTransactionRem(t *testing.T) {
	BeginTransaction()
	Cache("t21_mm", "test")
	Cache("t21_nn", "test")
	RemPrefixKey("t21_")
	CommitTransaction()
	if t1, err := GetCached("t21_mm"); err == nil || t1 == "test" {
		t.Error("Error commiting transaction")
	}
	if t1, err := GetCached("t21_nn"); err == nil || t1 == "test" {
		t.Error("Error in transaction cache")
	}
}

func TestTransactionRollback(t *testing.T) {
	BeginTransaction()
	Cache("t31_mm", "test")
	if t1, err := GetCached("t31_mm"); err == nil || t1 == "test" {
		t.Error("Error in transaction cache")
	}
	Cache("t32_mm", "test")
	RollbackTransaction()
	if t1, err := GetCached("t32_mm"); err == nil || t1 == "test" {
		t.Error("Error commiting transaction")
	}
	if t1, err := GetCached("t31_mm"); err == nil || t1 == "test" {
		t.Error("Error in transaction cache")
	}
}

func TestTransactionRemBefore(t *testing.T) {
	BeginTransaction()
	RemPrefixKey("t41_")
	Cache("t41_mm", "test")
	Cache("t41_nn", "test")
	CommitTransaction()
	if t1, err := GetCached("t41_mm"); err != nil || t1 != "test" {
		t.Error("Error commiting transaction")
	}
	if t1, err := GetCached("t41_nn"); err != nil || t1 != "test" {
		t.Error("Error in transaction cache")
	}
}

func TestRemPrefixKey(t *testing.T) {
	Cache("xxx_t1", "test")
	Cache("yyy_t1", "test")
	RemPrefixKey("xxx_")
	_, errX := GetCached("xxx_t1")
	_, errY := GetCached("yyy_t1")
	if errX == nil || errY != nil {
		t.Error("Error removing prefix: ", errX, errY)
	}
}

func TestCachePush(t *testing.T) {
	CachePush("ccc_t1", "1")
	CachePush("ccc_t1", "2")
	v, err := GetCached("ccc_t1")
	if err != nil || len(v.([]interface{})) != 2 {
		t.Error("Error in cache push: ", v)
	}
}

func TestCount(t *testing.T) {
	Cache("dst_A1", "1")
	Cache("dst_A2", "2")
	Cache("rpf_A3", "3")
	Cache("dst_A4", "4")
	Cache("dst_A5", "5")
	if CountEntries("dst_") != 4 {
		t.Error("Error countiong entries: ", CountEntries("dst_"))
	}
}
