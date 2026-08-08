package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/service"
)

// Requirement section 8: the front end must never control audit information.
//
// The guarantee is structural rather than a validation somebody has to
// remember to write — there is no field on any request type for a client to
// put an actor in, so there is nothing to strip and nothing to forget. These
// tests prove that, because "we removed the field" is the kind of claim that
// quietly stops being true when somebody adds a struct.

// auditUserFields are the names a client might reasonably try.
var auditUserFields = []string{
	"createdBy", "created_by", "changedBy", "changed_by",
	"updatedBy", "updated_by", "postedBy", "posted_by",
	"createdAt", "created_at", "changedAt", "changed_at",
}

// requestTypes is every shape the API decodes a body into.
func requestTypes() map[string]any {
	return map[string]any{
		"StorageLocationInput":        &service.StorageLocationInput{},
		"StorageProductCapacityInput": &service.StorageProductCapacityInput{},
		"PackagingTypeInput":          &service.PackagingTypeInput{},
		"ThresholdBandsInput":         &service.ThresholdBandsInput{},
		"MovementRequest":             &service.MovementRequest{},
		"ReservationRequest":          &service.ReservationRequest{},
		"StoragePlanLineInput":        &service.StoragePlanLineInput{},
		"UserInput":                   &service.UserInput{},
	}
}

func TestNoRequestTypeAcceptsAnAuditFieldFromJSON(t *testing.T) {
	for name, target := range requestTypes() {
		// A payload naming somebody else as the author, in every spelling.
		body := map[string]any{}
		for _, f := range auditUserFields {
			body[f] = "999"
		}
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}

		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		// Unknown fields are expected here — that is the whole point. What
		// must not happen is one of them *matching*.
		_ = dec.Decode(target)

		out, err := json.Marshal(target)
		if err != nil {
			t.Fatal(err)
		}
		var round map[string]any
		if err := json.Unmarshal(out, &round); err != nil {
			t.Fatal(err)
		}
		for _, f := range auditUserFields {
			if v, ok := round[f]; ok && v == "999" {
				t.Errorf("%s: a request body set %s to %v. The actor comes from the "+
					"session; a request type with a field for it is a request type "+
					"somebody can lie to.", name, f, v)
			}
		}
	}
}

// The same check from the other side: no JSON tag on any request type may name
// an audit field, whatever its Go field is called.
func TestNoRequestTypeExposesAnAuditFieldTag(t *testing.T) {
	for name, target := range requestTypes() {
		out, err := json.Marshal(target)
		if err != nil {
			t.Fatal(err)
		}
		var shape map[string]any
		if err := json.Unmarshal(out, &shape); err != nil {
			t.Fatal(err)
		}
		for _, f := range auditUserFields {
			if _, ok := shape[f]; ok {
				t.Errorf("%s serialises a %q field. Even read-only, a request type "+
					"that names the actor invites a client to send one.", name, f)
			}
		}
	}
}
