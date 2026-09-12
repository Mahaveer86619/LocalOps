package watchers

import (
	"context"

	"github.com/Mahaveer86619/LocalOps/internal/notify"
)

// Service wraps Store with the debounced Slack alerting rules from
// README §13.4: alert on the ok->down transition once it crosses
// FailThreshold consecutive failures (not on every failed check), and
// alert once on the down->ok recovery.
type Service struct {
	Store         *Store
	Notifier      *notify.Slack
	FailThreshold int // consecutive fails required before the first alert
}

func NewService(store *Store, notifier *notify.Slack, failThreshold int) *Service {
	if failThreshold <= 0 {
		failThreshold = 2
	}
	return &Service{Store: store, Notifier: notifier, FailThreshold: failThreshold}
}

// CheckIn records the outcome and fires a Slack alert exactly on the
// transitions that matter - never on every failing check, and never
// silently on recovery either.
//
// failThreshold optionally sets/updates the watcher's own alert threshold
// as part of this check-in (see Store.CheckIn) - pass nil to leave it
// unchanged. A watcher's own threshold, once set to a nonzero value,
// overrides the service-wide default: a continuous checker that already
// debounces locally (scripts/ping-watch.sh) registers with threshold 1 so
// its single "down" check-in per episode still alerts.
func (s *Service) CheckIn(ctx context.Context, name string, state State, message string, details map[string]any, failThreshold *int) (Watcher, error) {
	res, err := s.Store.CheckIn(ctx, name, state, message, details, failThreshold)
	if err != nil {
		return Watcher{}, err
	}
	w := res.Watcher

	effectiveThreshold := s.FailThreshold
	if w.FailThreshold > 0 {
		effectiveThreshold = w.FailThreshold
	}

	switch {
	case state == StateDown && w.ConsecutiveFails == effectiveThreshold:
		s.Notifier.WatcherDown(ctx, name, message)
	case state == StateOK && res.PreviousState == StateDown:
		s.Notifier.WatcherRecovered(ctx, name)
	}
	return w, nil
}
