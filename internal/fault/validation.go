package fault

import "strings"

const (
	RuleRequired    = "required"
	RuleRange       = "range"
	RulePositive    = "positive"
	RuleNonNegative = "non_negative"
	RuleMinCount    = "min_count"
	RuleDuplicate   = "duplicate"
	RuleUnknown     = "unknown"
	RuleFormat      = "format"
	RuleType        = "type"
	RuleCount       = "count"
	RuleZeroLength  = "zero_length"
	RuleSameService = "same_service"
	RuleNotMember   = "not_member"
)

type ValidationFault struct {
	Field   string `json:"field"`
	Index   *int   `json:"index,omitempty"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

type ValidationFaults []ValidationFault

func Index(i int) *int { return &i }

func Whole(field, rule, message string) ValidationFault {
	return ValidationFault{Field: field, Rule: rule, Message: message}
}

func At(field string, index int, rule, message string) ValidationFault {
	return ValidationFault{Field: field, Index: Index(index), Rule: rule, Message: message}
}

func (f ValidationFaults) Error() string {
	switch len(f) {
	case 0:
		return "validation failed"
	case 1:
		return f[0].Message
	}
	parts := make([]string, len(f))
	for i := range f {
		parts[i] = f[i].Message
	}
	return strings.Join(parts, "; ")
}

func (f ValidationFaults) Err() error {
	if len(f) == 0 {
		return nil
	}
	return f
}
