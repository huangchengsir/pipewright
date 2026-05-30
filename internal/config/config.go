// Package config loads platform runtime configuration from the environment.
//
// 单管理员实例的最小配置:监听地址、SQLite 数据库文件路径,以及凭据保险库的
// master key 来源。master key 缺失时保险库进入「未配置」态,平台仍正常启动。
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

// MasterKeyLen 是凭据保险库 master key 的字节长度(NaCl secretbox 需 32B)。
const MasterKeyLen = 32

// Config 是平台运行期配置。
type Config struct {
	// Addr 是 HTTP 监听地址,如 ":8080"。
	Addr string
	// DBPath 是 SQLite 数据库文件路径。
	DBPath string
	// AdminUsername 是管理员用户名(首次启动引导用);默认 "admin"。
	AdminUsername string
	// AdminPassword 是管理员初始口令(首次启动引导用);已存在管理员时忽略。
	// 注:此字段仅用于首次引导,不持久化,不入日志。
	AdminPassword string
}

// Load 从环境变量读取配置,缺失项回退到合理默认值。
func Load() Config {
	return Config{
		Addr:          getenv("DEVOPSTOOL_ADDR", ":8080"),
		DBPath:        getenv("DEVOPSTOOL_DB", "devopstool.db"),
		AdminUsername: getenv("DEVOPSTOOL_ADMIN_USERNAME", "admin"),
		AdminPassword: os.Getenv("DEVOPSTOOL_ADMIN_PASSWORD"), // 无默认值,空串表示未设置
	}
}

// ErrNoMasterKey 表示既未设置 DEVOPSTOOL_MASTER_KEY 也未设置 _FILE。
// 调用方据此让保险库进入「未配置」态,而非视为致命错误。
var ErrNoMasterKey = errors.New("config: no master key configured")

// LoadMasterKey 读取凭据保险库 master key。
//
// 优先级:DEVOPSTOOL_MASTER_KEY(base64 编码的 32 字节)> DEVOPSTOOL_MASTER_KEY_FILE
// (文件内容为 base64,允许尾随空白/换行)。两者皆未设置时返回 (nil, ErrNoMasterKey),
// 由调用方决定进入未配置态(不 panic)。
//
// 解码失败或长度不为 32B 时返回非 nil error(配置错误,应被察觉)。
// 错误信息绝不含 key 内容。
func LoadMasterKey() (*[MasterKeyLen]byte, error) {
	raw := os.Getenv("DEVOPSTOOL_MASTER_KEY")
	if raw == "" {
		if path := os.Getenv("DEVOPSTOOL_MASTER_KEY_FILE"); path != "" {
			b, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("config: read master key file: %w", err)
			}
			raw = string(b)
		}
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrNoMasterKey
	}

	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		// 不回显原始值。
		return nil, errors.New("config: master key is not valid base64")
	}
	if len(decoded) != MasterKeyLen {
		return nil, fmt.Errorf("config: master key must decode to %d bytes, got %d", MasterKeyLen, len(decoded))
	}
	var key [MasterKeyLen]byte
	copy(key[:], decoded)
	// 明文 key 副本用后清零(防 GC 前残留被 core dump/swap 捕获),与他处 zero() 纪律一致。
	for i := range decoded {
		decoded[i] = 0
	}
	return &key, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
