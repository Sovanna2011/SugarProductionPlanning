package service

import (
	"context"
	"fmt"
	"time"
)

// BusinessTimezoneKey names the parameter holding the factory's timezone.
const BusinessTimezoneKey = "BUSINESS_TIMEZONE"

// DefaultBusinessTimezone is the factory's own zone, used when the parameter
// row is missing — which should not happen, since migration 0008 writes it,
// but a server that will not start because a configuration row was deleted is
// worse than one that carries on with the documented default and says so.
const DefaultBusinessTimezone = "Asia/Phnom_Penh"

// SystemConfig is what the front end needs before it renders anything.
type SystemConfig struct {
	// BusinessTimezone is the IANA zone the site works in.
	//
	// Audit timestamps are instants, so they carry no zone of their own. But
	// "which day did that happen on" has to be answered somewhere, and
	// answering it in the reader's browser puts a posting made at 06:30 in
	// Kampong Speu on the previous day for anyone reading from Europe — and
	// the two of them then disagree about which day's production it belongs
	// to, with both screens telling the truth.
	//
	// So it is configuration, read from the database, and every screen renders
	// audit times in it.
	BusinessTimezone string `json:"businessTimezone"`

	// ServerTime is the authoritative clock, as an instant.
	//
	// The front end shows it so that a browser whose own clock is wrong — and
	// on a shared factory PC it often is — cannot leave somebody believing a
	// posting happened at a time it did not. The value written to created_at
	// comes from this clock, never from the device.
	ServerTime time.Time `json:"serverTime"`
}

// SystemConfig reads the configuration the front end needs.
func (s *Service) SystemConfig(ctx context.Context) (SystemConfig, error) {
	tz, err := s.store.SystemParameter(ctx, BusinessTimezoneKey)
	if err != nil || tz == "" {
		tz = DefaultBusinessTimezone
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return SystemConfig{}, fmt.Errorf(
			"the configured business timezone %q is not a zone this system knows: %w",
			tz, err)
	}
	return SystemConfig{BusinessTimezone: tz, ServerTime: time.Now()}, nil
}
