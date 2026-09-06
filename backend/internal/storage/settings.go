package storage

import (
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	SettingChannelSyncConfigured        = "sync.channel.configured"
	SettingChannelSyncEnabled           = "sync.channel.enabled"
	SettingChannelSyncInterval          = "sync.channel.interval_minutes"
	SettingRelayRateConfigured          = "sync.relay.rate.configured"
	SettingRelayRateEnabled             = "sync.relay.rate.enabled"
	SettingRelayRateInterval            = "sync.relay.rate.interval_minutes"
	SettingRelayRateIntervalSeconds     = "sync.relay.rate.interval_seconds"
	SettingRelaySnapshotConfigured      = "sync.relay.snapshot.configured"
	SettingRelaySnapshotEnabled         = "sync.relay.snapshot.enabled"
	SettingRelaySnapshotInterval        = "sync.relay.snapshot.interval_minutes"
	SettingRelaySnapshotIntervalSeconds = "sync.relay.snapshot.interval_seconds"

	settingLegacyRelaySyncConfigured = "sync.relay.configured"
	settingLegacyRelaySyncEnabled    = "sync.relay.enabled"
	settingLegacyRelaySyncInterval   = "sync.relay.interval_minutes"
)

type Settings struct{ db *gorm.DB }

func NewSettings(db *gorm.DB) *Settings { return &Settings{db: db} }

func (s *Settings) Get(key string) (string, bool, error) {
	var setting AppSetting
	err := s.db.First(&setting, "key = ?", key).Error
	if err == gorm.ErrRecordNotFound {
		return "", false, nil
	}
	return setting.Value, err == nil, err
}

func (s *Settings) Set(key, value string) error {
	return s.db.Where("key = ?", key).
		Assign(map[string]any{"value": value, "updated_at": time.Now().UTC()}).
		FirstOrCreate(&AppSetting{Key: key, Value: value}).Error
}

func (s *Settings) SyncSettings() (SyncSettings, error) {
	settings := SyncSettings{
		ChannelIntervalMinutes:       30,
		RelayRateIntervalMinutes:     1,
		RelayRateIntervalSeconds:     10,
		RelaySnapshotIntervalMinutes: 60,
		RelaySnapshotIntervalSeconds: 3600,
	}
	readBool := func(key string, target *bool) error {
		value, found, err := s.Get(key)
		if err != nil || !found {
			return err
		}
		*target, err = strconv.ParseBool(value)
		return err
	}
	readInt := func(key string, target *int) (bool, error) {
		value, found, err := s.Get(key)
		if err != nil || !found {
			return found, err
		}
		*target, err = strconv.Atoi(value)
		if err == nil && *target < 1 {
			*target = 1
		}
		return found, err
	}
	var err error
	if _, settings.ChannelConfigured, err = s.Get(SettingChannelSyncConfigured); err != nil {
		return settings, err
	}
	var legacyRelayConfigured bool
	if _, legacyRelayConfigured, err = s.Get(settingLegacyRelaySyncConfigured); err != nil {
		return settings, err
	}
	if err = readBool(SettingChannelSyncEnabled, &settings.ChannelEnabled); err != nil {
		return settings, err
	}
	var legacyRelayEnabled bool
	if err = readBool(settingLegacyRelaySyncEnabled, &legacyRelayEnabled); err != nil {
		return settings, err
	}
	if _, err = readInt(SettingChannelSyncInterval, &settings.ChannelIntervalMinutes); err != nil {
		return settings, err
	}
	legacyRelayInterval := 60
	legacyRelayIntervalConfigured, err := readInt(settingLegacyRelaySyncInterval, &legacyRelayInterval)
	if err != nil {
		return settings, err
	}
	if legacyRelayConfigured || legacyRelayIntervalConfigured {
		settings.RelayRateConfigured = legacyRelayConfigured
		settings.RelayRateEnabled = legacyRelayEnabled
		settings.RelayRateIntervalMinutes = legacyRelayInterval
		settings.RelayRateIntervalSeconds = legacyRelayInterval * 60
		settings.RelaySnapshotConfigured = legacyRelayConfigured
		settings.RelaySnapshotEnabled = legacyRelayEnabled
		settings.RelaySnapshotIntervalMinutes = legacyRelayInterval
		settings.RelaySnapshotIntervalSeconds = legacyRelayInterval * 60
	}

	if _, configured, getErr := s.Get(SettingRelayRateConfigured); getErr != nil {
		return settings, getErr
	} else if configured {
		settings.RelayRateConfigured = true
		if err = readBool(SettingRelayRateEnabled, &settings.RelayRateEnabled); err != nil {
			return settings, err
		}
		if configured, readErr := readInt(SettingRelayRateInterval, &settings.RelayRateIntervalMinutes); readErr != nil {
			return settings, readErr
		} else if configured {
			settings.RelayRateIntervalSeconds = settings.RelayRateIntervalMinutes * 60
		}
		if _, configured, getErr := s.Get(SettingRelayRateIntervalSeconds); getErr != nil {
			return settings, getErr
		} else if configured {
			if _, readErr := readInt(SettingRelayRateIntervalSeconds, &settings.RelayRateIntervalSeconds); readErr != nil {
				return settings, readErr
			}
		}
	}
	if _, configured, getErr := s.Get(SettingRelaySnapshotConfigured); getErr != nil {
		return settings, getErr
	} else if configured {
		settings.RelaySnapshotConfigured = true
		if err = readBool(SettingRelaySnapshotEnabled, &settings.RelaySnapshotEnabled); err != nil {
			return settings, err
		}
		if _, readErr := readInt(SettingRelaySnapshotInterval, &settings.RelaySnapshotIntervalMinutes); readErr != nil {
			err = readErr
			return settings, err
		}
		settings.RelaySnapshotIntervalSeconds = settings.RelaySnapshotIntervalMinutes * 60
		if _, secondsConfigured, getErr := s.Get(SettingRelaySnapshotIntervalSeconds); getErr != nil {
			return settings, getErr
		} else if secondsConfigured {
			if _, readErr := readInt(SettingRelaySnapshotIntervalSeconds, &settings.RelaySnapshotIntervalSeconds); readErr != nil {
				err = readErr
				return settings, err
			}
		}
	}
	return settings, nil
}

func (s *Settings) SaveSyncSettings(settings SyncSettings) error {
	if settings.ChannelIntervalMinutes < 1 {
		settings.ChannelIntervalMinutes = 30
	}
	if settings.RelayRateIntervalSeconds < 5 {
		if settings.RelayRateIntervalMinutes > 0 {
			settings.RelayRateIntervalSeconds = settings.RelayRateIntervalMinutes * 60
		} else {
			settings.RelayRateIntervalSeconds = 10
		}
	}
	if settings.RelayRateIntervalSeconds < 5 {
		settings.RelayRateIntervalSeconds = 10
	}
	settings.RelayRateIntervalMinutes = (settings.RelayRateIntervalSeconds + 59) / 60
	if settings.RelaySnapshotIntervalSeconds < 5 {
		if settings.RelaySnapshotIntervalMinutes > 0 {
			settings.RelaySnapshotIntervalSeconds = settings.RelaySnapshotIntervalMinutes * 60
		} else {
			settings.RelaySnapshotIntervalSeconds = 3600
		}
	}
	settings.RelaySnapshotIntervalMinutes = (settings.RelaySnapshotIntervalSeconds + 59) / 60
	values := map[string]string{
		SettingChannelSyncConfigured:        "true",
		SettingChannelSyncEnabled:           strconv.FormatBool(settings.ChannelEnabled),
		SettingChannelSyncInterval:          strconv.Itoa(settings.ChannelIntervalMinutes),
		SettingRelayRateConfigured:          "true",
		SettingRelayRateEnabled:             strconv.FormatBool(settings.RelayRateEnabled),
		SettingRelayRateInterval:            strconv.Itoa(settings.RelayRateIntervalMinutes),
		SettingRelayRateIntervalSeconds:     strconv.Itoa(settings.RelayRateIntervalSeconds),
		SettingRelaySnapshotConfigured:      "true",
		SettingRelaySnapshotEnabled:         strconv.FormatBool(settings.RelaySnapshotEnabled),
		SettingRelaySnapshotInterval:        strconv.Itoa(settings.RelaySnapshotIntervalMinutes),
		SettingRelaySnapshotIntervalSeconds: strconv.Itoa(settings.RelaySnapshotIntervalSeconds),
	}
	for key, value := range values {
		if err := s.Set(key, strings.TrimSpace(value)); err != nil {
			return err
		}
	}
	return nil
}
