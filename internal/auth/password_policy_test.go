package auth_test

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/fault"
)

func TestPasswordFaultRuleNames(t *testing.T) {
	if fault.RuleRequired != "required" ||
		fault.RuleMinLength != "min_length" ||
		fault.RuleMaxLength != "max_length" ||
		fault.RuleMatchesEmail != "matches_email" ||
		fault.RuleCommon != "common" {
		t.Fatalf("rule names = required:%q min:%q max:%q email:%q common:%q",
			fault.RuleRequired, fault.RuleMinLength, fault.RuleMaxLength,
			fault.RuleMatchesEmail, fault.RuleCommon)
	}
}

func TestValidatePassword(t *testing.T) {
	const (
		eleven = "eleven-char"
		twelve = "twelve-chars"
		email  = "person@example.com"
	)
	if utf8.RuneCountInString(eleven) != 11 || utf8.RuneCountInString(twelve) != 12 {
		t.Fatalf("fixture lengths: eleven=%d twelve=%d",
			utf8.RuneCountInString(eleven), utf8.RuneCountInString(twelve))
	}

	seventyTwo := strings.Repeat("q", 72)
	seventyThree := strings.Repeat("q", 73)
	runes72 := strings.Repeat("é", 36) // 2 bytes each
	runes74 := strings.Repeat("é", 37)

	tests := []struct {
		name     string
		pw       string
		email    string
		rules    []string
		messages []string
	}{
		{"11 characters", eleven, email,
			[]string{fault.RuleMinLength},
			[]string{"password must be at least 12 characters"}},
		{"11 runes", strings.Repeat("é", 11), email,
			[]string{fault.RuleMinLength},
			[]string{"password must be at least 12 characters"}},
		{"12 characters", twelve, email, nil, nil},
		{"12 runes", strings.Repeat("é", 12), email, nil, nil},
		{"72 bytes", seventyTwo, email, nil, nil},
		{"72 bytes of multibyte runes", runes72, email, nil, nil},
		{"73 bytes", seventyThree, email,
			[]string{fault.RuleMaxLength},
			[]string{"password must be at most 72 bytes"}},
		{"over 72 bytes of multibyte runes", runes74, email,
			[]string{fault.RuleMaxLength},
			[]string{"password must be at most 72 bytes"}},
		{"common", "password123456", email,
			[]string{fault.RuleCommon},
			[]string{"password is too common"}},
		{"common any case", "Password123456", email,
			[]string{fault.RuleCommon},
			[]string{"password is too common"}},
		{"not a substring of a common password", "password123456x", email, nil, nil},
		{"matches email", "Unique-Mailbox@Example.com", "  unique-mailbox@example.com ",
			[]string{fault.RuleMatchesEmail},
			[]string{"password must not match the email address"}},
		{"matches email any case", "unique-mailbox@example.com", "UNIQUE-MAILBOX@EXAMPLE.COM",
			[]string{fault.RuleMatchesEmail},
			[]string{"password must not match the email address"}},
		{"does not trim the password", " twelve-chars", "twelve-chars", nil, nil},
		{"blank email skips the match", twelve, "  \t", nil, nil},
		{"empty", "", email,
			[]string{fault.RuleRequired},
			[]string{"password is required"}},
		{"empty password and empty email", "", "   ",
			[]string{fault.RuleRequired},
			[]string{"password is required"}},
		{"several faults", "123456", " 123456 ",
			[]string{fault.RuleMinLength, fault.RuleMatchesEmail, fault.RuleCommon},
			[]string{
				"password must be at least 12 characters",
				"password must not match the email address",
				"password is too common",
			}},
		{"max length and email together", seventyThree, "  " + seventyThree + "  ",
			[]string{fault.RuleMaxLength, fault.RuleMatchesEmail},
			[]string{
				"password must be at most 72 bytes",
				"password must not match the email address",
			}},
		{"fixture their-password", "their-password", email, nil, nil},
		{"fixture member-password", "member-password", email, nil, nil},
		{"fixture owner-password", "owner-password", email, nil, nil},
		{"fixture stranger-password", "stranger-password", email, nil, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := auth.ValidatePassword(tt.pw, tt.email)
			if tt.rules == nil {
				if err != nil {
					t.Fatalf("ValidatePassword() = %v, want nil", err)
				}
				return
			}
			faults := mustPasswordFaults(t, err)
			if len(faults) != len(tt.rules) {
				t.Fatalf("got %d faults (%v), want %v", len(faults), faultRules(faults), tt.rules)
			}
			for i, f := range faults {
				if f.Rule != tt.rules[i] || f.Message != tt.messages[i] {
					t.Errorf("fault %d = %+v, want rule %s message %q",
						i, f, tt.rules[i], tt.messages[i])
				}
			}
			if tt.pw != "" && strings.Contains(err.Error(), tt.pw) {
				t.Errorf("error %q contains the password", err.Error())
			}
			if tt.name == "73 bytes" && !strings.Contains(err.Error(), "72") {
				t.Errorf("error %q does not mention the 72-byte limit", err.Error())
			}
		})
	}
}

func mustPasswordFaults(t *testing.T, err error) fault.ValidationFaults {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var faults fault.ValidationFaults
	if !errors.As(err, &faults) {
		t.Fatalf("errors.As(%v, *fault.ValidationFaults) = false", err)
	}
	if len(faults) == 0 {
		t.Fatal("ValidationFaults is empty")
	}
	for i, f := range faults {
		if f.Field != "password" {
			t.Errorf("fault %d field = %q, want password", i, f.Field)
		}
	}
	return faults
}

func faultRules(faults fault.ValidationFaults) []string {
	rules := make([]string, len(faults))
	for i, f := range faults {
		rules[i] = f.Rule
	}
	return rules
}
