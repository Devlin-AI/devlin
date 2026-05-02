package tool

import (
	"regexp"
)

var (
	ansiCSI   = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	ansiOSC   = regexp.MustCompile(`\x1b\][^\x07]*\x07`)
	ansiOther = regexp.MustCompile(`\x1b\([A-Za-z]`)
)

func stripANSI(s string) string {
	s = ansiCSI.ReplaceAllString(s, "")
	s = ansiOSC.ReplaceAllString(s, "")
	s = ansiOther.ReplaceAllString(s, "")
	return s
}
