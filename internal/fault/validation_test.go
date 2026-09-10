package fault_test

import (
	"errors"
	"testing"

	"github.com/andrewcgraves/sparks-effect-api/internal/fault"
)

func TestValidationFaultsErrIsNilWhenEmpty(t *testing.T) {
	var faults fault.ValidationFaults
	if err := faults.Err(); err != nil {
		t.Fatalf("empty faults.Err() = %v, want nil", err)
	}
}

func TestValidationFaultsErrorJoinsMessages(t *testing.T) {
	one := fault.ValidationFaults{fault.Whole("name", fault.RuleRequired, "name is required")}
	if got := one.Error(); got != "name is required" {
		t.Errorf("single fault Error() = %q", got)
	}

	two := fault.ValidationFaults{
		fault.At("stops.lat", 0, fault.RuleRange, "stop 0: lat must be between -90 and 90"),
		fault.At("stops.lng", 1, fault.RuleRange, "stop 1: lng must be between -180 and 180"),
	}
	want := "stop 0: lat must be between -90 and 90; stop 1: lng must be between -180 and 180"
	if got := two.Error(); got != want {
		t.Errorf("joined Error() = %q, want %q", got, want)
	}
}

func TestValidationFaultsRoundTripThroughErrorsAs(t *testing.T) {
	err := fault.ValidationFaults{
		fault.At("stops.lat", 3, fault.RuleRange, "stop 3: lat must be between -90 and 90"),
	}.Err()

	var faults fault.ValidationFaults
	if !errors.As(err, &faults) {
		t.Fatalf("errors.As(%v, *ValidationFaults) = false", err)
	}
	if len(faults) != 1 || faults[0].Field != "stops.lat" || faults[0].Rule != fault.RuleRange {
		t.Fatalf("faults = %+v, want stops.lat/range", faults)
	}
	if faults[0].Index == nil || *faults[0].Index != 3 {
		t.Fatalf("index = %v, want 3", faults[0].Index)
	}
}
