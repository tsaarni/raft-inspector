package main

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/encoding/protowire"
)

func loadRootKey(initFile string) ([]byte, error) {
	data, err := os.ReadFile(initFile)
	if err != nil {
		return nil, fmt.Errorf("reading init file: %w", err)
	}
	var init struct {
		UnsealKeysB64   []string `json:"unseal_keys_b64"`
		UnsealThreshold int      `json:"unseal_threshold"`
	}
	if err := json.Unmarshal(data, &init); err != nil {
		return nil, fmt.Errorf("parsing init file: %w", err)
	}
	if init.UnsealThreshold > 1 {
		return nil, fmt.Errorf("unseal_threshold=%d not supported (only threshold=1 is supported)", init.UnsealThreshold)
	}
	if len(init.UnsealKeysB64) == 0 {
		return nil, fmt.Errorf("no unseal keys found in init file")
	}
	key, err := base64.StdEncoding.DecodeString(init.UnsealKeysB64[0])
	if err != nil {
		return nil, fmt.Errorf("decoding unseal key: %w", err)
	}
	return key, nil
}

func aesGCMDecrypt(key, ciphertext []byte, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	return gcm.Open(nil, ciphertext[:nonceSize], ciphertext[nonceSize:], aad)
}

func decryptBarrierEntry(key []byte, path string, raw []byte) ([]byte, error) {
	if len(raw) < 5 {
		return nil, fmt.Errorf("entry too short (%d bytes)", len(raw))
	}
	version := raw[4]
	payload := raw[5:]
	var aad []byte
	if version == 0x02 {
		aad = []byte(path)
	}
	return aesGCMDecrypt(key, payload, aad)
}

// nonBarrierKeys lists storage paths that are written directly to the physical
// backend without barrier encryption. Attempting to decrypt them as barrier
// entries would misinterpret their raw bytes as an encryption term.
var nonBarrierKeys = map[string]string{
	"core/lock":                    "not encrypted (raw leader UUID written directly to physical storage)",
	"core/hsm/barrier-unseal-keys": "encrypted with seal key, not barrier (protobuf-wrapped BlobInfo)",
}

func decryptEntry(keys map[uint32][]byte, path string, raw []byte) ([]byte, error) {
	if msg, ok := nonBarrierKeys[path]; ok {
		if path == "core/lock" {
			// core/lock is a raw UUID string, return it directly.
			return raw, nil
		}
		return nil, fmt.Errorf("skipped: %s", msg)
	}
	if len(raw) < 5 {
		return nil, fmt.Errorf("entry too short")
	}
	term := binary.BigEndian.Uint32(raw[:4])
	key, ok := keys[term]
	if !ok {
		return nil, fmt.Errorf("no key for term %d", term)
	}
	return decryptBarrierEntry(key, path, raw)
}

func loadKeyring(rootKey []byte, db *bolt.DB) (map[uint32][]byte, error) {
	var raw []byte
	var storedKeysRaw []byte
	db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("data"))
		if b == nil {
			return fmt.Errorf("data bucket not found")
		}
		if v := b.Get([]byte("core/keyring")); v != nil {
			raw = make([]byte, len(v))
			copy(raw, v)
		}
		if v := b.Get([]byte("core/hsm/barrier-unseal-keys")); v != nil {
			storedKeysRaw = make([]byte, len(v))
			copy(storedKeysRaw, v)
		}
		return nil
	})
	if raw == nil {
		return nil, fmt.Errorf("core/keyring not found in data bucket")
	}

	actualRootKey := rootKey
	if storedKeysRaw != nil {
		ciphertext := extractBlobInfoCiphertext(storedKeysRaw)
		if ciphertext == nil {
			return nil, fmt.Errorf("failed to parse stored keys protobuf")
		}
		plaintext, err := aesGCMDecrypt(rootKey, ciphertext, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: stored-keys decryption failed (%v), trying unseal key directly as root key\n", err)
		} else {
			var keys [][]byte
			if err := json.Unmarshal(plaintext, &keys); err != nil {
				return nil, fmt.Errorf("parsing stored keys JSON: %w", err)
			}
			if len(keys) != 1 {
				return nil, fmt.Errorf("expected 1 stored key, got %d", len(keys))
			}
			actualRootKey = keys[0]
		}
	}

	plaintext, err := decryptBarrierEntry(actualRootKey, "core/keyring", raw)
	if err != nil {
		return nil, fmt.Errorf("decrypting keyring: %w", err)
	}

	var keyring struct {
		Keys []struct {
			Term  uint32 `json:"Term"`
			Value []byte `json:"Value"`
		} `json:"Keys"`
	}
	if err := json.Unmarshal(plaintext, &keyring); err != nil {
		return nil, fmt.Errorf("parsing keyring JSON: %w", err)
	}
	keys := make(map[uint32][]byte)
	for _, k := range keyring.Keys {
		keys[k.Term] = k.Value
	}
	return keys, nil
}

func loadKeyringFromStateBin(rootKey []byte, data []byte) (map[uint32][]byte, error) {
	entries := map[string][]byte{}
	buf := data
	for len(buf) > 0 {
		msgLen, n := protowire.ConsumeVarint(buf)
		if n < 0 {
			break
		}
		buf = buf[n:]
		if uint64(len(buf)) < msgLen {
			break
		}
		key, val := parseStorageEntry(buf[:msgLen])
		buf = buf[msgLen:]
		if key == "core/keyring" || key == "core/hsm/barrier-unseal-keys" {
			entries[key] = val
		}
	}
	raw := entries["core/keyring"]
	if raw == nil {
		return nil, fmt.Errorf("core/keyring not found in state.bin")
	}

	actualRootKey := rootKey
	if storedKeysRaw := entries["core/hsm/barrier-unseal-keys"]; storedKeysRaw != nil {
		ciphertext := extractBlobInfoCiphertext(storedKeysRaw)
		if ciphertext != nil {
			plaintext, err := aesGCMDecrypt(rootKey, ciphertext, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: stored-keys decryption failed (%v), trying unseal key directly as root key\n", err)
			} else {
				var keys [][]byte
				if err := json.Unmarshal(plaintext, &keys); err == nil && len(keys) == 1 {
					actualRootKey = keys[0]
				}
			}
		}
	}

	plaintext, err := decryptBarrierEntry(actualRootKey, "core/keyring", raw)
	if err != nil {
		return nil, fmt.Errorf("decrypting keyring: %w", err)
	}
	var keyring struct {
		Keys []struct {
			Term  uint32 `json:"Term"`
			Value []byte `json:"Value"`
		} `json:"Keys"`
	}
	if err := json.Unmarshal(plaintext, &keyring); err != nil {
		return nil, fmt.Errorf("parsing keyring JSON: %w", err)
	}
	keys := make(map[uint32][]byte)
	for _, k := range keyring.Keys {
		keys[k.Term] = k.Value
	}
	return keys, nil
}

func extractBlobInfoCiphertext(data []byte) []byte {
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return nil
		}
		data = data[n:]
		switch typ {
		case protowire.BytesType:
			v, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return nil
			}
			data = data[n:]
			if num == 1 {
				return v
			}
		case protowire.VarintType:
			_, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return nil
			}
			data = data[n:]
		default:
			n := protowire.ConsumeFieldValue(num, typ, data)
			if n < 0 {
				return nil
			}
			data = data[n:]
		}
	}
	return nil
}

func parseStorageEntry(data []byte) (key string, val []byte) {
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return
		}
		data = data[n:]
		switch typ {
		case protowire.BytesType:
			v, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return
			}
			data = data[n:]
			switch num {
			case 1:
				key = string(v)
			case 2:
				val = v
			}
		default:
			n := protowire.ConsumeFieldValue(num, typ, data)
			if n < 0 {
				return
			}
			data = data[n:]
		}
	}
	return
}
