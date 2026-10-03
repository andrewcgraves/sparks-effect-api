package auth

import (
	"bufio"
	_ "embed"
	"strings"
	"unicode/utf8"

	"github.com/andrewcgraves/sparks-effect-api/internal/fault"
)

// common_passwords.txt is the SecLists 10k-most-common password list (MIT).

//go:embed common_passwords.txt
var commonPasswordsTxt string

var commonPasswords = parseCommonPasswords(commonPasswordsTxt)

func parseCommonPasswords(text string) map[string]struct{} {
	set := make(map[string]struct{}, 10000)
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			continue
		}
		set[strings.ToLower(line)] = struct{}{}
	}
	if err := sc.Err(); err != nil {
		panic("auth: reading common passwords: " + err.Error())
	}
	return set
}

func ValidatePassword(pw, email string) error {
	if pw == "" {
		return fault.ValidationFaults{
			fault.Whole("password", fault.RuleRequired, "password is required"),
		}.Err()
	}

	var faults fault.ValidationFaults
	if utf8.RuneCountInString(pw) < 12 {
		faults = append(faults, fault.Whole("password", fault.RuleMinLength,
			"password must be at least 12 characters"))
	}
	// The 72-byte cap is bcrypt's input limit, so a longer password is
	// rejected rather than stored.
	if len(pw) > 72 {
		faults = append(faults, fault.Whole("password", fault.RuleMaxLength,
			"password must be at most 72 bytes"))
	}
	if trimmed := strings.TrimSpace(email); trimmed != "" && strings.EqualFold(pw, trimmed) {
		faults = append(faults, fault.Whole("password", fault.RuleMatchesEmail,
			"password must not match the email address"))
	}
	if _, ok := commonPasswords[strings.ToLower(pw)]; ok {
		faults = append(faults, fault.Whole("password", fault.RuleCommon,
			"password is too common"))
	}
	return faults.Err()
}
