package monetizeapi

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestServiceOffer_IsDraining(t *testing.T) {
	t.Run("nil drainAt", func(t *testing.T) {
		o := &ServiceOffer{}
		if o.IsDraining() {
			t.Errorf("IsDraining() = true, want false for nil drainAt")
		}
	})
	t.Run("set drainAt", func(t *testing.T) {
		now := metav1.Now()
		o := &ServiceOffer{Spec: ServiceOfferSpec{DrainAt: &now}}
		if !o.IsDraining() {
			t.Errorf("IsDraining() = false, want true for non-nil drainAt")
		}
	})
}

func TestServiceOffer_DrainEndsAt(t *testing.T) {
	base := time.Date(2026, time.May, 1, 12, 0, 0, 0, time.UTC)
	baseMeta := metav1.NewTime(base)

	cases := []struct {
		name  string
		drain *metav1.Time
		grace *metav1.Duration
		want  time.Time
	}{
		{
			name:  "nil drainAt returns zero",
			drain: nil,
			grace: nil,
			want:  time.Time{},
		},
		{
			name:  "nil grace applies default 1h",
			drain: &baseMeta,
			grace: nil,
			want:  base.Add(time.Hour),
		},
		{
			name:  "explicit zero grace honored",
			drain: &baseMeta,
			grace: &metav1.Duration{Duration: 0},
			want:  base,
		},
		{
			name:  "custom grace honored",
			drain: &baseMeta,
			grace: &metav1.Duration{Duration: 30 * time.Minute},
			want:  base.Add(30 * time.Minute),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := &ServiceOffer{Spec: ServiceOfferSpec{DrainAt: tc.drain, DrainGracePeriod: tc.grace}}
			if got := o.DrainEndsAt(); !got.Equal(tc.want) {
				t.Errorf("DrainEndsAt() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestServiceOffer_DrainExpired(t *testing.T) {
	now := time.Date(2026, time.May, 1, 12, 0, 0, 0, time.UTC)

	t.Run("not draining returns false", func(t *testing.T) {
		o := &ServiceOffer{}
		if o.DrainExpired(now) {
			t.Errorf("DrainExpired() = true, want false for non-draining offer")
		}
	})

	t.Run("mid-drain returns false", func(t *testing.T) {
		drainAt := metav1.NewTime(now.Add(-10 * time.Minute))
		o := &ServiceOffer{Spec: ServiceOfferSpec{
			DrainAt:          &drainAt,
			DrainGracePeriod: &metav1.Duration{Duration: time.Hour},
		}}
		if o.DrainExpired(now) {
			t.Errorf("DrainExpired() = true, want false for mid-drain offer")
		}
	})

	t.Run("expired returns true", func(t *testing.T) {
		drainAt := metav1.NewTime(now.Add(-2 * time.Hour))
		o := &ServiceOffer{Spec: ServiceOfferSpec{
			DrainAt:          &drainAt,
			DrainGracePeriod: &metav1.Duration{Duration: time.Hour},
		}}
		if !o.DrainExpired(now) {
			t.Errorf("DrainExpired() = false, want true for expired drain")
		}
	})

	t.Run("force path zero grace tears down on next reconcile", func(t *testing.T) {
		drainAt := metav1.NewTime(now)
		o := &ServiceOffer{Spec: ServiceOfferSpec{
			DrainAt:          &drainAt,
			DrainGracePeriod: &metav1.Duration{Duration: 0},
		}}
		if !o.DrainExpired(now) {
			t.Errorf("DrainExpired() = false at now == drainAt with zero grace, want true")
		}
	})
}
