package vault

import (
	"crypto/rand"
	"errors"
	"fmt"

	"golang.org/x/crypto/nacl/secretbox"
)

// 加密原语:NaCl secretbox(XSalsa20-Poly1305)。
// master key 32B;每次 Seal 生成随机 24B nonce 并前置于密文,Open 时从头部取回。
// 认证标签由 secretbox 内置:错误的 key 或被篡改的密文在 Open 时校验失败。

const (
	// keySize 是 secretbox 主密钥长度(32 字节)。
	keySize = 32
	// nonceSize 是 secretbox nonce 长度(24 字节)。
	nonceSize = 24
)

// ErrDecrypt 表示密文解密/认证失败(错误 master key 或密文被篡改)。
// 故意不携带任何明文或密钥信息。
var ErrDecrypt = errors.New("vault: decrypt failed")

// seal 用 master key 加密明文,返回 nonce||box 的字节序列。
// nonce 来自 crypto/rand;绝不复用。
func seal(key *[keySize]byte, plaintext []byte) ([]byte, error) {
	var nonce [nonceSize]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("vault: generate nonce: %w", err)
	}
	// 输出预置 nonce;secretbox.Seal 追加密文+认证标签。
	out := secretbox.Seal(nonce[:], plaintext, &nonce, key)
	return out, nil
}

// open 从 nonce||box 中取回明文;认证失败返回 ErrDecrypt(不泄漏细节)。
func open(key *[keySize]byte, sealed []byte) ([]byte, error) {
	if len(sealed) < nonceSize {
		return nil, ErrDecrypt
	}
	var nonce [nonceSize]byte
	copy(nonce[:], sealed[:nonceSize])
	plaintext, ok := secretbox.Open(nil, sealed[nonceSize:], &nonce, key)
	if !ok {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}
