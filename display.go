package main

import (
	"encoding/hex"
	"fmt"
	"unicode/utf8"

	"github.com/fatih/color"
	"google.golang.org/protobuf/encoding/protowire"
)

var (
	header  = color.New(color.FgCyan, color.Bold)
	label   = color.New(color.FgYellow)
	value   = color.New(color.FgWhite)
	opColor = color.New(color.FgGreen)
	keyCol  = color.New(color.FgMagenta)
	dim     = color.New(color.FgHiBlack)
)

var suffrageNames = map[int32]string{
	0: "voter",
	1: "nonvoter",
	2: "staging",
}

func suffrageName(s int32) string {
	if name, ok := suffrageNames[s]; ok {
		return name
	}
	return fmt.Sprintf("unknown(%d)", s)
}

var opNames = map[uint32]string{
	1:   "delete",
	2:   "put",
	4:   "restoreCallback",
	8:   "get",
	16:  "verifyRead",
	32:  "verifyList",
	64:  "beginTx",
	128: "commitTx",
}

func opName(op uint32) string {
	if name, ok := opNames[op]; ok {
		return name
	}
	return "unknown"
}

func containsControlChars(b []byte) bool {
	for _, c := range b {
		if c < 0x20 && c != '\n' && c != '\r' && c != '\t' {
			return true
		}
	}
	return false
}

func printDecryptedValue(keys map[uint32][]byte, path string, raw []byte, maxLen int) {
	plaintext, err := decryptEntry(keys, path, raw)
	if err != nil {
		dim.Printf("      [decrypt error: %v]\n", err)
		return
	}
	printValue(plaintext, maxLen, "      ")
}

// extractFirstTextField tries to parse data as a protobuf message and returns
// the first bytes field that is valid printable UTF-8 text.
func extractFirstTextField(data []byte) []byte {
	buf := data
	for len(buf) > 0 {
		_, typ, n := protowire.ConsumeTag(buf)
		if n < 0 {
			return nil
		}
		buf = buf[n:]
		switch typ {
		case protowire.BytesType:
			v, n := protowire.ConsumeBytes(buf)
			if n < 0 {
				return nil
			}
			buf = buf[n:]
			if len(v) > 0 && utf8.Valid(v) && !containsControlChars(v) {
				return v
			}
		case protowire.VarintType:
			_, n := protowire.ConsumeVarint(buf)
			if n < 0 {
				return nil
			}
			buf = buf[n:]
		default:
			return nil
		}
	}
	return nil
}

func printValue(plaintext []byte, maxLen int, indent string) {
	display := plaintext
	// If not directly printable, try to unwrap protobuf to find readable text.
	if !utf8.Valid(display) || containsControlChars(display) {
		if inner := extractFirstTextField(display); inner != nil {
			display = inner
		}
	}

	truncated := false
	if maxLen > 0 && len(display) > maxLen {
		display = display[:maxLen]
		truncated = true
	}

	if utf8.Valid(display) && !containsControlChars(display) {
		value.Printf("%s%s", indent, string(display))
	} else {
		dim.Printf("%s[hex] %s", indent, hex.EncodeToString(display))
	}

	if truncated {
		dim.Printf(" [...truncated, %d bytes total]", len(plaintext))
	}
	fmt.Println()
}
