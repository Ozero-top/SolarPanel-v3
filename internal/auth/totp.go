package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// TOTP 实现（RFC 6238），纯 crypto 无外部依赖

// TOTPSecretNew 生成 160-bit 随机密钥并返回 Base32 编码
func TOTPSecretNew() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf), nil
}

// TOTPCode 计算当前时间片的 6 位动态码
func TOTPCode(secret string, t time.Time) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", err
	}
	counter := uint64(t.Unix() / 30)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	truncated := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	code := truncated % 1000000
	return fmt.Sprintf("%06d", code), nil
}

// TOTPVerify 验证动态码，容忍 ±1 时间窗口
func TOTPVerify(secret, code string) bool {
	now := time.Now()
	for delta := -1; delta <= 1; delta++ {
		t := now.Add(time.Duration(delta*30) * time.Second)
		got, err := TOTPCode(secret, t)
		if err != nil {
			continue
		}
		if hmac.Equal([]byte(got), []byte(code)) {
			return true
		}
	}
	return false
}

// TOTPProvisioningURL 生成 OTPAuth URL（用于 QR 码）
func TOTPProvisioningURL(secret, label, issuer string) string {
	return fmt.Sprintf("otpauth://totp/%s?secret=%s&issuer=%s", label, secret, issuer)
}
