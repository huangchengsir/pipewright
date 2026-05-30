// Package auth 实现单管理员认证(argon2id 哈希、会话管理、登录失败锁定)。
// 此包仅经 repository/service 接口访问 DB,不直接操作 HTTP。
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// ErrIncompatibleVersion 表示 PHC 字串声明的 argon2 版本与本地不一致。
var ErrIncompatibleVersion = errors.New("auth: incompatible argon2 version")

// argonParams 保存从 PHC 字串解析出的可变参数,用于校验时按相同参数重算。
type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
	keyLen  uint32
}

// argon2id 参数:业界稳妥默认值(time=1, memory=64MB, threads=4, keyLen=32, saltLen=16)。
// 参考 https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html
const (
	argonTime    uint32 = 1
	argonMemory  uint32 = 64 * 1024 // 64MB
	argonThreads uint8  = 4
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

// ErrInvalidHash 表示 PHC 字串格式不合法。
var ErrInvalidHash = errors.New("auth: invalid argon2id hash format")

// HashPassword 使用 argon2id 对 plain 哈希,返回 PHC 格式字串:
//
//	$argon2id$v=19$m=65536,t=1,p=4$<base64 salt>$<base64 hash>
//
// 每次调用生成新的随机 salt,确保相同口令哈希值不同。
func HashPassword(plain string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generate salt: %w", err)
	}
	hash := argon2.IDKey([]byte(plain), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64Salt, b64Hash), nil
}

// VerifyPassword 用恒定时间比较校验 plain 是否与 PHC 字串 encoded 匹配。
// 恒定时间比较(crypto/subtle)防时序侧信道。
func VerifyPassword(plain, encoded string) (bool, error) {
	params, salt, hash, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	// 用 PHC 串里声明的参数重算(参数无关校验),而非硬编码常量。
	candidate := argon2.IDKey([]byte(plain), salt, params.time, params.memory, params.threads, params.keyLen)
	if subtle.ConstantTimeCompare(hash, candidate) != 1 {
		return false, nil
	}
	return true, nil
}

// decodeHash 解析 PHC 格式字串,返回参数、salt 与期望 hash。
// 格式:$argon2id$v=19$m=65536,t=1,p=4$<b64salt>$<b64hash>
// 校验时按解析出的参数重算,避免硬编码常量变更后无法校验既有 hash。
func decodeHash(encoded string) (params argonParams, salt, hash []byte, err error) {
	parts := strings.Split(encoded, "$")
	// 分割后:["", "argon2id", "v=19", "m=...,t=...,p=...", "<b64salt>", "<b64hash>"]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return params, nil, nil, ErrInvalidHash
	}

	var version int
	if _, err = fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return params, nil, nil, fmt.Errorf("%w: %v", ErrInvalidHash, err)
	}
	if version != argon2.Version {
		return params, nil, nil, fmt.Errorf("%w: got %d, want %d", ErrIncompatibleVersion, version, argon2.Version)
	}

	if _, err = fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.memory, &params.time, &params.threads); err != nil {
		return params, nil, nil, fmt.Errorf("%w: %v", ErrInvalidHash, err)
	}

	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return params, nil, nil, fmt.Errorf("%w: %v", ErrInvalidHash, err)
	}
	hash, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return params, nil, nil, fmt.Errorf("%w: %v", ErrInvalidHash, err)
	}
	// keyLen 由实际 hash 长度决定。
	params.keyLen = uint32(len(hash))
	return params, salt, hash, nil
}
