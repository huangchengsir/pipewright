// Package artifactstore 是「构建产物物理存储」(制品库 · Story 8-16 / FR-8-16)。
//
// 背景:Epic 3 的构建把 jar/dist **定位**出来并记元数据(run_artifacts:type/name/reference/size),
// 但产物**字节**随临时工作区一起删了 —— Epic 4 部署只能拿到 reference 串(占位),无法真正部署。
// 本包补这一层:构建后把 jar 文件 / dist 打包的字节**拷出工作区存盘**,产物 reference 改存「存储句柄」,
// 部署时按句柄取**真字节**写到目标机。
//
// 设计:**内容寻址**(content-addressed)磁盘库 —— 字节经 SHA-256 落到 <root>/<2 位前缀>/<全 hash>,
// 相同内容天然去重(同一产物多次构建不重复占盘)。无外部依赖、无 init 副作用;root 由 main 注入
// (默认 DB 同级 artifacts/,env PIPEWRIGHT_ARTIFACT_DIR 可改)。句柄即 hash 串,Open 前严校(防路径穿越)。
package artifactstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// 领域错误。错误体不含字节内容 / 绝对路径外的敏感信息。
var (
	// ErrInvalidKey 表示存储句柄非法(非 64 位小写 hex;防路径穿越)。
	ErrInvalidKey = errors.New("artifactstore: invalid key")
	// ErrNotFound 表示该句柄无对应产物。
	ErrNotFound = errors.New("artifactstore: artifact not found")
)

// Store 是内容寻址磁盘制品库。零依赖;并发安全(Put 经临时文件 + 原子 rename;Open 只读)。
type Store struct {
	root string
}

// New 构造制品库,root 不存在则创建(0700:产物可能含敏感构建输出,限本用户)。
func New(root string) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("artifactstore: empty root")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("artifactstore: mkdir root: %w", err)
	}
	return &Store{root: root}, nil
}

// Put 把 r 的字节流式落库,返回内容寻址句柄(sha256 hex)与字节数。
// 经临时文件 + 原子 rename 落盘:并发/中断不产生半截文件;内容相同 → 同句柄(去重)。
func (s *Store) Put(r io.Reader) (key string, size int64, err error) {
	tmp, err := os.CreateTemp(s.root, ".put-*")
	if err != nil {
		return "", 0, fmt.Errorf("artifactstore: temp: %w", err)
	}
	tmpName := tmp.Name()
	// 失败路径清理临时文件(成功路径已 rename 走,Remove 无害)。
	defer func() { _ = os.Remove(tmpName) }()

	h := sha256.New()
	size, err = io.Copy(io.MultiWriter(tmp, h), r)
	if err != nil {
		_ = tmp.Close()
		return "", 0, fmt.Errorf("artifactstore: copy: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return "", 0, fmt.Errorf("artifactstore: close temp: %w", err)
	}

	key = hex.EncodeToString(h.Sum(nil))
	dest := s.pathFor(key)
	if err = os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return "", 0, fmt.Errorf("artifactstore: mkdir shard: %w", err)
	}
	// 已存在(同内容)→ 直接复用,不重复写(去重)。
	if _, statErr := os.Stat(dest); statErr == nil {
		return key, size, nil
	}
	if err = os.Rename(tmpName, dest); err != nil {
		return "", 0, fmt.Errorf("artifactstore: commit: %w", err)
	}
	return key, size, nil
}

// Open 按句柄打开产物只读流(调用方负责 Close)。句柄非法 → ErrInvalidKey;不存在 → ErrNotFound。
func (s *Store) Open(key string) (io.ReadCloser, error) {
	if !validKey(key) {
		return nil, ErrInvalidKey
	}
	f, err := os.Open(s.pathFor(key))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return f, nil
}

// Stat 返回产物字节数(用于校验 / 展示)。句柄非法 → ErrInvalidKey;不存在 → ErrNotFound。
func (s *Store) Stat(key string) (int64, error) {
	if !validKey(key) {
		return 0, ErrInvalidKey
	}
	fi, err := os.Stat(s.pathFor(key))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return fi.Size(), nil
}

// Has 报告句柄是否已存在(非法句柄视为不存在,不报错)。
func (s *Store) Has(key string) bool {
	if !validKey(key) {
		return false
	}
	_, err := os.Stat(s.pathFor(key))
	return err == nil
}

// pathFor 把句柄映射为分片磁盘路径:<root>/<前 2 位>/<全 hash>(防单目录文件过多)。
func (s *Store) pathFor(key string) string {
	return filepath.Join(s.root, key[:2], key)
}

// validKey 校验句柄为 64 位小写 hex(sha256);杜绝 ../ 等路径穿越。
func validKey(key string) bool {
	if len(key) != 64 {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
