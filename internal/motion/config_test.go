package motion

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMotionConfig_Validate(t *testing.T) {
	cases := []struct {
		name    string
		mc      MotionConfig
		wantErr string
	}{
		{
			name: "default-is-valid",
			mc:   DefaultMotionConfig(),
		},
		{
			name: "explicit-onvif",
			mc: MotionConfig{
				Enabled: true, Source: MotionConfigSourceONVIF,
				Sensitivity: 50, CooldownMS: 5000,
			},
		},
		{
			name: "explicit-local-future",
			mc: MotionConfig{
				Enabled: true, Source: MotionConfigSourceLocalFuture,
				Sensitivity: 0, CooldownMS: 0,
			},
		},
		{
			name: "bad-source",
			mc: MotionConfig{
				Source: MotionConfigSource("magic-cv"),
			},
			wantErr: "invalid motion source",
		},
		{
			name: "bad-sensitivity-high",
			mc: MotionConfig{
				Source: MotionConfigSourceONVIF, Sensitivity: 101,
			},
			wantErr: "out of [0, 100]",
		},
		{
			name: "bad-sensitivity-low",
			mc: MotionConfig{
				Source: MotionConfigSourceONVIF, Sensitivity: -1,
			},
			wantErr: "out of [0, 100]",
		},
		{
			name: "bad-cooldown",
			mc: MotionConfig{
				Source: MotionConfigSourceONVIF, CooldownMS: -100,
			},
			wantErr: "must be non-negative",
		},
		{
			name: "roi-out-of-bounds",
			mc: MotionConfig{
				Source: MotionConfigSourceONVIF,
				ROI:    &MotionROI{X: 0.5, Y: 0.5, W: 0.8, H: 0.8},
			},
			wantErr: "extends beyond frame",
		},
		{
			name: "roi-valid",
			mc: MotionConfig{
				Source: MotionConfigSourceONVIF,
				ROI:    &MotionROI{X: 0.1, Y: 0.1, W: 0.5, H: 0.5},
			},
		},
		{
			name: "schedule-bad-day",
			mc: MotionConfig{
				Source: MotionConfigSourceONVIF,
				Schedule: &MotionSchedule{
					Timezone: "UTC",
					Windows: []MotionScheduleWindow{
						{Days: []string{"funday"}, Start: "08:00", End: "17:00"},
					},
				},
			},
			wantErr: "invalid day",
		},
		{
			name: "schedule-bad-clock",
			mc: MotionConfig{
				Source: MotionConfigSourceONVIF,
				Schedule: &MotionSchedule{
					Timezone: "UTC",
					Windows: []MotionScheduleWindow{
						{Days: []string{"monday"}, Start: "no-time", End: "17:00"},
					},
				},
			},
			wantErr: "invalid start",
		},
		{
			name: "schedule-valid",
			mc: MotionConfig{
				Source: MotionConfigSourceONVIF,
				Schedule: &MotionSchedule{
					Timezone: "UTC",
					Windows: []MotionScheduleWindow{
						{Days: []string{"monday", "tuesday"}, Start: "08:00", End: "17:00"},
					},
				},
			},
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			err := c.mc.Validate()
			if c.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.Contains(t, err.Error(), c.wantErr)
			}
		})
	}
}

func TestMotionConfig_NormalizeFillsDefaults(t *testing.T) {
	mc := MotionConfig{Enabled: true}
	mc.Normalize()
	require.Equal(t, MotionConfigSourceONVIF, mc.Source)
	require.Equal(t, DefaultSensitivity, mc.Sensitivity)
	require.Equal(t, int(DefaultCooldown/time.Millisecond), mc.CooldownMS)
}

func TestMotionConfig_CooldownReturnsDuration(t *testing.T) {
	mc := MotionConfig{CooldownMS: 1500}
	require.Equal(t, 1500*time.Millisecond, mc.Cooldown())

	mc2 := MotionConfig{}
	require.Equal(t, DefaultCooldown, mc2.Cooldown(), "zero CooldownMS should fall back to default")
}
