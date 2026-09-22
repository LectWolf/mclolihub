package qoderproxy

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Qoder's CLI encrypts the session key to this public key. It is a protocol
// constant, not a secret.
const rsaPublicKeyPEM = `-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDA8iMH5c02LilrsERw9t6Pv5Nc
4k6Pz1EaDicBMpdpxKduSZu5OANqUq8er4GM95omAGIOPOh+Nx0spthYA2BqGz+l
6HRkPJ7S236FZz73In/KVuLnwI8JJ2CbuJap8kvheCCZpmAWpb/cPx/3Vr/J6I17
XcW+ML9FoCI6AOvOzwIDAQAB
-----END PUBLIC KEY-----`

const (
	cosyVersion  = "1.0.0"
	clientType   = "5"
	dataPolicy   = "disagree"
	loginVersion = "v2"
	machineOS    = "x86_64_windows"
	machineType  = "5"
	clientIP     = "127.0.0.1"
)

// Creds is the identity COSY attaches to one request.
type Creds struct {
	UserID    string
	AuthToken string
	Name      string
	Email     string
	MachineID string
}

// Headers is the Authorization value plus the Cosy-* header map.
type Headers struct {
	Authorization string
	Headers       map[string]string
}

var rsaPublic *rsa.PublicKey

func init() {
	block, _ := pem.Decode([]byte(rsaPublicKeyPEM))
	if block == nil {
		panic("qoderproxy: invalid RSA public key PEM")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		panic("qoderproxy: parse RSA public key: " + err.Error())
	}
	var ok bool
	rsaPublic, ok = pub.(*rsa.PublicKey)
	if !ok {
		panic("qoderproxy: public key is not RSA")
	}
}

// Sign builds the COSY header set over body and requestURL.
func Sign(body []byte, requestURL string, creds Creds) (*Headers, error) {
	return signAt(body, requestURL, creds, time.Now(), newID)
}

func signAt(body []byte, requestURL string, creds Creds, now time.Time, nextID func() string) (*Headers, error) {
	if strings.TrimSpace(creds.UserID) == "" {
		return nil, fmt.Errorf("cosy: user id is empty")
	}
	if strings.TrimSpace(creds.AuthToken) == "" {
		return nil, fmt.Errorf("cosy: auth token is empty")
	}
	if nextID == nil {
		nextID = newID
	}

	aesKeyText := nextID()
	if len(aesKeyText) < 16 {
		return nil, fmt.Errorf("cosy: aes key source is shorter than 16")
	}
	aesKey := []byte(aesKeyText[:16])

	userInfo, err := json.Marshal(map[string]string{
		"aid":                  "",
		"email":                creds.Email,
		"name":                 creds.Name,
		"security_oauth_token": creds.AuthToken,
		"uid":                  creds.UserID,
	})
	if err != nil {
		return nil, fmt.Errorf("cosy: user info: %w", err)
	}
	infoB64, err := aesEncryptCBCBase64(userInfo, aesKey)
	if err != nil {
		return nil, fmt.Errorf("cosy: aes encrypt: %w", err)
	}
	keyB64, err := rsaEncryptBase64(aesKey)
	if err != nil {
		return nil, fmt.Errorf("cosy: rsa encrypt: %w", err)
	}

	timestamp := fmt.Sprintf("%d", now.Unix())
	requestID := nextID()
	payloadJSON, err := json.Marshal(map[string]string{
		"cosyVersion": cosyVersion,
		"ideVersion":  "",
		"info":        infoB64,
		"requestId":   requestID,
		"version":     "v1",
	})
	if err != nil {
		return nil, fmt.Errorf("cosy: payload: %w", err)
	}
	payloadB64 := base64.StdEncoding.EncodeToString(payloadJSON)
	sigPath := SigPath(requestURL)
	sigInput := strings.Join([]string{payloadB64, keyB64, timestamp, string(body), sigPath}, "\n")
	signature := md5Hex([]byte(sigInput))

	machineID := strings.TrimSpace(creds.MachineID)
	if machineID == "" {
		machineID = nextID()
	}

	return &Headers{
		Authorization: "Bearer COSY." + payloadB64 + "." + signature,
		Headers: map[string]string{
			"Cosy-Key":               keyB64,
			"Cosy-User":              creds.UserID,
			"Cosy-Date":              timestamp,
			"Cosy-Version":           cosyVersion,
			"Cosy-Machineid":         machineID,
			"Cosy-Machinetoken":      machineID,
			"Cosy-Machinetype":       machineType,
			"Cosy-Machineos":         machineOS,
			"Cosy-Clienttype":        clientType,
			"Cosy-Clientip":          clientIP,
			"Cosy-Bodyhash":          md5Hex(body),
			"Cosy-Bodylength":        fmt.Sprintf("%d", len(body)),
			"Cosy-Sigpath":           sigPath,
			"Cosy-Data-Policy":       dataPolicy,
			"Cosy-Organization-Id":   "",
			"Cosy-Organization-Tags": "",
			"Login-Version":          loginVersion,
			"X-Request-Id":           nextID(),
		},
	}, nil
}

// SigPath is the URL path after the first "/algo", without the query.
func SigPath(requestURL string) string {
	idx := strings.Index(requestURL, "/algo")
	if idx < 0 {
		return ""
	}
	path := requestURL[idx+len("/algo"):]
	if q := strings.IndexByte(path, '?'); q >= 0 {
		path = path[:q]
	}
	return path
}

func newID() string {
	return uuid.NewString()
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	pad := blockSize - (len(data) % blockSize)
	if pad == 0 {
		pad = blockSize
	}
	return append(data, bytes.Repeat([]byte{byte(pad)}, pad)...)
}

func aesEncryptCBCBase64(plaintext, key []byte) (string, error) {
	if len(key) != aes.BlockSize {
		return "", fmt.Errorf("aes key must be %d bytes, got %d", aes.BlockSize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	padded := pkcs7Pad(plaintext, aes.BlockSize)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, key).CryptBlocks(out, padded)
	return base64.StdEncoding.EncodeToString(out), nil
}

func rsaEncryptBase64(data []byte) (string, error) {
	enc, err := rsa.EncryptPKCS1v15(rand.Reader, rsaPublic, data)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(enc), nil
}

func md5Hex(data []byte) string {
	sum := md5.Sum(data) // #nosec G401 -- COSY signs with MD5; this is not a password hash.
	return hex.EncodeToString(sum[:])
}
