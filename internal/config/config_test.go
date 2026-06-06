package config

import (
	"encoding/base64"
	"testing"
)

// TestLoadMasterKeyDecodes 验证有效 base64 32B key 正确解码。
func TestLoadMasterKeyDecodes(t *testing.T) {
	var raw [MasterKeyLen]byte
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	t.Setenv("PIPEWRIGHT_MASTER_KEY", base64.StdEncoding.EncodeToString(raw[:]))
	t.Setenv("PIPEWRIGHT_MASTER_KEY_FILE", "")

	key, err := LoadMasterKey()
	if err != nil {
		t.Fatalf("LoadMasterKey: %v", err)
	}
	if *key != raw {
		t.Fatalf("解码后的 key 与原值不一致")
	}
}

// TestLoadMasterKeyNoConfig 验证两源皆缺 → ErrNoMasterKey(不 panic)。
func TestLoadMasterKeyNoConfig(t *testing.T) {
	t.Setenv("PIPEWRIGHT_MASTER_KEY", "")
	t.Setenv("PIPEWRIGHT_MASTER_KEY_FILE", "")
	if _, err := LoadMasterKey(); err != ErrNoMasterKey {
		t.Fatalf("want ErrNoMasterKey, got %v", err)
	}
}

// TestLoadMasterKeyBadBase64 验证非法 base64 报错且不回显原值。
func TestLoadMasterKeyBadBase64(t *testing.T) {
	t.Setenv("PIPEWRIGHT_MASTER_KEY", "!!!not-base64!!!")
	t.Setenv("PIPEWRIGHT_MASTER_KEY_FILE", "")
	_, err := LoadMasterKey()
	if err == nil {
		t.Fatalf("非法 base64 应报错")
	}
	if got := err.Error(); got == "" || containsAny(got, "!!!not-base64!!!") {
		t.Fatalf("错误信息不应回显原始 key 值: %q", got)
	}
}

// TestLoadMasterKeyWrongLength 验证长度非 32B 报错。
func TestLoadMasterKeyWrongLength(t *testing.T) {
	t.Setenv("PIPEWRIGHT_MASTER_KEY", base64.StdEncoding.EncodeToString([]byte("short")))
	t.Setenv("PIPEWRIGHT_MASTER_KEY_FILE", "")
	if _, err := LoadMasterKey(); err == nil {
		t.Fatalf("长度错误应报错")
	}
}

func containsAny(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestStoreConfig 验证 driver/dsn 推导:mysql 必须有 DSN;sqlite 回退 DBPath。
func TestStoreConfig(t *testing.T) {
	cases := []struct {
		name                string
		cfg                 Config
		wantDriver, wantDSN string
		wantErr             bool
	}{
		{"default sqlite path", Config{DBDriver: "sqlite", DBPath: "p.db"}, "sqlite", "p.db", false},
		{"empty driver falls back sqlite", Config{DBPath: "p.db"}, "sqlite", "p.db", false},
		{"sqlite prefers dsn", Config{DBDriver: "sqlite", DBDSN: "x.db", DBPath: "p.db"}, "sqlite", "x.db", false},
		{"mysql with dsn", Config{DBDriver: "mysql", DBDSN: "u:p@tcp(h:3306)/db"}, "mysql", "u:p@tcp(h:3306)/db", false},
		{"mysql without dsn errors", Config{DBDriver: "mysql"}, "", "", true},
		{"unknown driver errors", Config{DBDriver: "postgres"}, "", "", true},
	}
	for _, c := range cases {
		d, dsn, err := c.cfg.StoreConfig()
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v wantErr=%v", c.name, err, c.wantErr)
			continue
		}
		if !c.wantErr && (d != c.wantDriver || dsn != c.wantDSN) {
			t.Errorf("%s: got (%q,%q) want (%q,%q)", c.name, d, dsn, c.wantDriver, c.wantDSN)
		}
	}
}
