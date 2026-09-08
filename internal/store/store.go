package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/docker/secrets-engine/store"
	"github.com/docker/secrets-engine/store/keychain"
	"github.com/docker/secrets-engine/store/posixage"
	"github.com/docker/secrets-engine/x/secrets"
	"go.etcd.io/bbolt"
)

// PassphraseFunc is called to obtain a passphrase for encrypting or decrypting
// the age-encrypted fallback store. The prompt string describes what is being
// asked (e.g. "Enter passphrase" vs "Confirm passphrase").
type PassphraseFunc func(ctx context.Context, prompt string) ([]byte, error)

const (
	ServiceName = "gotidal"
	AccountName = "session"
	DBFile      = "gotidal-cache.db"

	// oldServiceName/oldDBFile are gotidal's former name (tidalt), kept only
	// so an existing install migrates its login session and cache in place
	// on first run rather than losing them to the rename.
	oldServiceName = "tidalt"
	oldDBFile      = "tidal-cache.db"
)

func dbPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return DBFile
	}
	dir := filepath.Join(home, ".local", "share", ServiceName)
	_ = os.MkdirAll(dir, 0o700)
	return filepath.Join(dir, DBFile)
}

// migrateDataDirs moves ~/.config/tidalt and ~/.local/share/tidalt to their
// gotidal equivalents on first run after the tidalt→gotidal rename, so an
// existing login session and cache aren't lost. Safe to call on every
// startup — a no-op once the new directories exist. Failures are silent:
// worst case the user re-authenticates or rebuilds the cache, which is mild
// inconvenience, not data corruption.
func migrateDataDirs() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	migrateDir(filepath.Join(home, ".config", oldServiceName), filepath.Join(home, ".config", ServiceName))

	shareNew := filepath.Join(home, ".local", "share", ServiceName)
	migrateDir(filepath.Join(home, ".local", "share", oldServiceName), shareNew)

	// The directory above may have carried the bbolt file over under its old
	// name; rename it too if so.
	newDB := filepath.Join(shareNew, DBFile)
	oldDB := filepath.Join(shareNew, oldDBFile)
	if _, err := os.Stat(newDB); err == nil {
		return
	}
	if _, err := os.Stat(oldDB); err == nil {
		_ = os.Rename(oldDB, newDB)
	}
}

// migrateDir renames oldDir to newDir if newDir doesn't exist yet but oldDir
// does — a one-time, best-effort move.
func migrateDir(oldDir, newDir string) {
	if _, err := os.Stat(newDir); err == nil {
		return // already migrated, or never existed under the old name
	}
	if _, err := os.Stat(oldDir); err != nil {
		return // nothing to migrate
	}
	_ = os.Rename(oldDir, newDir)
}

// migrateKeychainSession copies a session secret stored under the old
// (tidalt) keychain service name into the new one, if the new store doesn't
// already have one. Best-effort: any failure here just means the user logs
// in again — no data is lost or corrupted either way.
func migrateKeychainSession(s store.Store) {
	if s == nil {
		return
	}
	ctx := context.Background()
	if _, err := s.Get(ctx, secrets.MustParseID(AccountName)); err == nil {
		return // already has a session under the new name
	}
	old, err := keychain.New(oldServiceName, AccountName, tidalSecretFactory)
	if err != nil {
		return
	}
	secret, err := old.Get(ctx, secrets.MustParseID(AccountName))
	if err != nil {
		return
	}
	_ = s.Upsert(ctx, secrets.MustParseID(AccountName), secret)
}

// SecretsStore handles secure storage using the docker/secrets-engine keychain or posixage fallback.
type SecretsStore struct {
	store store.Store
	db    *bbolt.DB
}

// tidalSecret implements the store.Secret interface
type tidalSecret struct {
	Data []byte
}

func (s *tidalSecret) Marshal() ([]byte, error) { return s.Data, nil }
func (s *tidalSecret) Unmarshal(data []byte) error {
	s.Data = data
	return nil
}
func (s *tidalSecret) Metadata() map[string]string              { return nil }
func (s *tidalSecret) SetMetadata(meta map[string]string) error { return nil }

func tidalSecretFactory(ctx context.Context, id store.ID) *tidalSecret {
	return &tidalSecret{}
}

// NewClientStore opens only the secrets backend (keychain / posixage) without
// the bbolt database. Use this in client mode where the parent process already
// holds the exclusive DB lock.
func NewClientStore(passphrase PassphraseFunc) *SecretsStore {
	migrateDataDirs()

	var s store.Store
	var err error

	s, err = keychain.New(ServiceName, AccountName, tidalSecretFactory)
	if err == nil {
		migrateKeychainSession(s)
	}
	if err != nil {
		home, _ := os.UserHomeDir()
		storePath := filepath.Join(home, ".config", ServiceName, "secrets")
		_ = os.MkdirAll(storePath, 0o700)

		root, rErr := os.OpenRoot(storePath)
		if rErr != nil {
			fmt.Printf("Error: failed to open root for posixage: %v\n", rErr)
		} else {
			encryptFn := posixage.EncryptionPassword(func(ctx context.Context) ([]byte, error) {
				return passphrase(ctx, "Enter passphrase for secret store")
			})
			decryptFn := posixage.DecryptionPassword(func(ctx context.Context) ([]byte, error) {
				return passphrase(ctx, "Enter passphrase for secret store")
			})
			s, err = posixage.New(root, tidalSecretFactory,
				posixage.WithEncryptionCallbackFunc(encryptFn),
				posixage.WithDecryptionCallbackFunc(decryptFn),
			)
			if err != nil {
				fmt.Printf("Error: failed to initialize posixage: %v\n", err)
			}
		}
	}

	return &SecretsStore{store: s}
}

func NewSecretsStore(passphrase PassphraseFunc) *SecretsStore {
	migrateDataDirs()

	var s store.Store
	var err error

	// 1. Try Keychain
	s, err = keychain.New(ServiceName, AccountName, tidalSecretFactory)
	if err == nil {
		migrateKeychainSession(s)
	}
	if err != nil {
		fmt.Printf("Warning: failed to initialize keychain: %v. Falling back to posixage.\n", err)

		// 2. Fallback to Posixage — requires encryption/decryption callbacks.
		home, _ := os.UserHomeDir()
		storePath := filepath.Join(home, ".config", ServiceName, "secrets")
		_ = os.MkdirAll(storePath, 0o700)

		root, rErr := os.OpenRoot(storePath)
		if rErr != nil {
			fmt.Printf("Error: failed to open root for posixage: %v\n", rErr)
		} else {
			encryptFn := posixage.EncryptionPassword(func(ctx context.Context) ([]byte, error) {
				return passphrase(ctx, "Enter passphrase for secret store")
			})
			decryptFn := posixage.DecryptionPassword(func(ctx context.Context) ([]byte, error) {
				return passphrase(ctx, "Enter passphrase for secret store")
			})
			s, err = posixage.New(root, tidalSecretFactory,
				posixage.WithEncryptionCallbackFunc(encryptFn),
				posixage.WithDecryptionCallbackFunc(decryptFn),
			)
			if err != nil {
				fmt.Printf("Error: failed to initialize posixage: %v\n", err)
			}
		}
	}

	db, err := bbolt.Open(dbPath(), 0o600, &bbolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		fmt.Printf("Warning: failed to open bolt db: %v\n", err)
	} else {
		if err := db.Update(func(tx *bbolt.Tx) error {
			for _, name := range []string{"Tracks", "Settings", "Cache", "Lyrics"} {
				if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			fmt.Printf("Warning: failed to initialize bolt db buckets: %v\n", err)
		}
	}

	return &SecretsStore{store: s, db: db}
}

func (s *SecretsStore) SaveSession(data any) error {
	if s.store == nil {
		return errors.New("no secure store initialized")
	}
	bytes, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return s.store.Upsert(context.Background(), secrets.MustParseID(AccountName), &tidalSecret{Data: bytes})
}

func (s *SecretsStore) LoadSession(target any) error {
	if s.store == nil {
		return errors.New("no secure store initialized")
	}
	secret, err := s.store.Get(context.Background(), secrets.MustParseID(AccountName))
	if err != nil {
		return err
	}
	bytes, err := secret.Marshal()
	if err != nil {
		return err
	}
	return json.Unmarshal(bytes, target)
}

func (s *SecretsStore) DeleteSession() error {
	if s.store == nil {
		return errors.New("no secure store initialized")
	}
	return s.store.Delete(context.Background(), secrets.MustParseID(AccountName))
}

func (s *SecretsStore) CacheTrack(trackID int, data any) error {
	if s.db == nil {
		return nil
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("Tracks"))
		bytes, err := json.Marshal(data)
		if err != nil {
			return err
		}
		return b.Put(fmt.Appendf(nil, "%d", trackID), bytes)
	})
}

// CacheLyrics stores raw lyrics data (LRC synced text, plain text, or neither)
// for a track. Callers cache a "not found" result too (an empty raw string
// with found=false) so a track with no lyrics isn't re-queried against LRCLIB
// every time it's hovered or played.
func (s *SecretsStore) CacheLyrics(trackID int, raw string, found bool) error {
	if s.db == nil {
		return nil
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("Lyrics"))
		if b == nil {
			return nil
		}
		bytes, err := json.Marshal(struct {
			Raw   string `json:"raw"`
			Found bool   `json:"found"`
		}{Raw: raw, Found: found})
		if err != nil {
			return err
		}
		return b.Put(fmt.Appendf(nil, "%d", trackID), bytes)
	})
}

// GetCachedLyrics returns a previously cached lyrics lookup for trackID.
// ok reports whether an entry exists at all (hit or cached miss); found
// reports whether that entry represents an actual lyrics match.
func (s *SecretsStore) GetCachedLyrics(trackID int) (raw string, found, ok bool) {
	if s.db == nil {
		return "", false, false
	}
	_ = s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("Lyrics"))
		if b == nil {
			return nil
		}
		v := b.Get(fmt.Appendf(nil, "%d", trackID))
		if v == nil {
			return nil
		}
		var entry struct {
			Raw   string `json:"raw"`
			Found bool   `json:"found"`
		}
		if err := json.Unmarshal(v, &entry); err != nil {
			return nil //nolint:nilerr // a corrupt cache entry is treated as a cache miss
		}
		raw, found, ok = entry.Raw, entry.Found, true
		return nil
	})
	return raw, found, ok
}

func (s *SecretsStore) SaveDevice(hwName string) error {
	if s.db == nil {
		return nil
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("Settings"))
		if b == nil {
			return nil
		}
		return b.Put([]byte("device"), []byte(hwName))
	})
}

func (s *SecretsStore) LoadDevice() (string, error) {
	if s.db == nil {
		return "", nil
	}
	var device string
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("Settings"))
		if b == nil {
			return nil
		}
		v := b.Get([]byte("device"))
		if v != nil {
			device = string(v)
		}
		return nil
	})
	return device, err
}

// SaveInterTrackSilenceMs persists the inter-track silence gap (milliseconds;
// 0 = gapless, the default) — an advanced, off-by-default setting for feeding
// a downstream recorder's own silence-based auto-track-detection.
func (s *SecretsStore) SaveInterTrackSilenceMs(ms uint32) error {
	if s.db == nil {
		return nil
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("Settings"))
		if b == nil {
			return nil
		}
		return b.Put([]byte("interTrackSilenceMs"), fmt.Appendf(nil, "%d", ms))
	})
}

// LoadInterTrackSilenceMs returns the persisted inter-track silence gap, or 0
// (gapless) if none has been saved yet.
func (s *SecretsStore) LoadInterTrackSilenceMs() (uint32, error) {
	if s.db == nil {
		return 0, nil
	}
	var ms uint32
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("Settings"))
		if b == nil {
			return nil
		}
		v := b.Get([]byte("interTrackSilenceMs"))
		if v != nil {
			_, _ = fmt.Sscanf(string(v), "%d", &ms)
		}
		return nil
	})
	return ms, err
}

// SaveBitPerfectMode persists whether playback opens the ALSA hw: device
// directly for bit-perfect output (true, the default) or instead opens the
// ALSA "default" PCM — normally PipeWire's own plugin — so any output
// PipeWire manages (laptop speakers, HDMI, Bluetooth, ...) is usable without
// a recognized DAC connected, at the cost of bit-perfectness.
func (s *SecretsStore) SaveBitPerfectMode(on bool) error {
	if s.db == nil {
		return nil
	}
	v := "1"
	if !on {
		v = "0"
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("Settings"))
		if b == nil {
			return nil
		}
		return b.Put([]byte("bitPerfectMode"), []byte(v))
	})
}

// LoadBitPerfectMode returns the persisted bit-perfect setting, defaulting to
// true (on) when nothing has been saved yet.
func (s *SecretsStore) LoadBitPerfectMode() (bool, error) {
	if s.db == nil {
		return true, nil
	}
	on := true
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("Settings"))
		if b == nil {
			return nil
		}
		v := b.Get([]byte("bitPerfectMode"))
		if v != nil {
			on = string(v) != "0"
		}
		return nil
	})
	return on, err
}

// SaveTheme persists the selected color-scheme name (a key into the UI's
// palette registry) so the chosen theme survives across launches.
func (s *SecretsStore) SaveTheme(name string) error {
	if s.db == nil {
		return nil
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("Settings"))
		if b == nil {
			return nil
		}
		return b.Put([]byte("theme"), []byte(name))
	})
}

// LoadTheme returns the saved color-scheme name, or "" if none has been set
// (the caller falls back to the default palette).
func (s *SecretsStore) LoadTheme() (string, error) {
	if s.db == nil {
		return "", nil
	}
	var theme string
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("Settings"))
		if b == nil {
			return nil
		}
		v := b.Get([]byte("theme"))
		if v != nil {
			theme = string(v)
		}
		return nil
	})
	return theme, err
}

func (s *SecretsStore) SaveVolume(vol float64) error {
	if s.db == nil {
		return nil
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("Settings"))
		if b == nil {
			return nil
		}
		return b.Put([]byte("volume"), fmt.Appendf(nil, "%f", vol))
	})
}

func (s *SecretsStore) LoadVolume() (float64, error) {
	if s.db == nil {
		return 100.0, nil
	}
	vol := 100.0
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("Settings"))
		if b == nil {
			return nil
		}
		v := b.Get([]byte("volume"))
		if v == nil {
			return nil
		}
		_, err := fmt.Sscanf(string(v), "%f", &vol)
		return err
	})
	return vol, err
}

func (s *SecretsStore) SaveLastPosition(seconds float64) error {
	if s.db == nil {
		return nil
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("Settings"))
		if b == nil {
			return nil
		}
		return b.Put([]byte("lastPosition"), fmt.Appendf(nil, "%f", seconds))
	})
}

func (s *SecretsStore) LoadLastPosition() (float64, error) {
	if s.db == nil {
		return 0, nil
	}
	var pos float64
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("Settings"))
		if b == nil {
			return nil
		}
		v := b.Get([]byte("lastPosition"))
		if v == nil {
			return nil
		}
		_, err := fmt.Sscanf(string(v), "%f", &pos)
		return err
	})
	return pos, err
}

func (s *SecretsStore) SaveLastTrackID(trackID int) error {
	if s.db == nil {
		return nil
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("Settings"))
		if b == nil {
			return nil
		}
		return b.Put([]byte("lastTrackID"), fmt.Appendf(nil, "%d", trackID))
	})
}

func (s *SecretsStore) LoadLastTrackID() (int, error) {
	if s.db == nil {
		return 0, nil
	}
	var id int
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("Settings"))
		if b == nil {
			return nil
		}
		v := b.Get([]byte("lastTrackID"))
		if v == nil {
			return nil
		}
		_, err := fmt.Sscanf(string(v), "%d", &id)
		return err
	})
	return id, err
}

// SavePlaylist persists the current track list so it can be restored on next
// startup.
func (s *SecretsStore) SavePlaylist(tracks any) error {
	if s.db == nil {
		return nil
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("Settings"))
		if b == nil {
			return nil
		}
		data, err := json.Marshal(tracks)
		if err != nil {
			return err
		}
		return b.Put([]byte("playlist"), data)
	})
}

// LoadPlaylist restores the track list saved by the previous session.
// Returns nil, nil when no playlist is stored yet.
func (s *SecretsStore) LoadPlaylist(target any) error {
	if s.db == nil {
		return nil
	}
	return s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("Settings"))
		if b == nil {
			return nil
		}
		v := b.Get([]byte("playlist"))
		if v == nil {
			return nil
		}
		return json.Unmarshal(v, target)
	})
}

// SaveHistory persists the recently-played track list so it survives across
// sessions.
func (s *SecretsStore) SaveHistory(tracks any) error {
	if s.db == nil {
		return nil
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("Settings"))
		if b == nil {
			return nil
		}
		data, err := json.Marshal(tracks)
		if err != nil {
			return err
		}
		return b.Put([]byte("history"), data)
	})
}

// LoadHistory restores the recently-played list saved by a previous session.
// Returns without error when no history is stored yet.
func (s *SecretsStore) LoadHistory(target any) error {
	if s.db == nil {
		return nil
	}
	return s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("Settings"))
		if b == nil {
			return nil
		}
		v := b.Get([]byte("history"))
		if v == nil {
			return nil
		}
		return json.Unmarshal(v, target)
	})
}

// CacheSearchResults stores the tracks returned for a search query so they can
// be served from cache on repeated lookups.
func (s *SecretsStore) CacheSearchResults(query string, tracks any) error {
	if s.db == nil {
		return nil
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("Cache"))
		if b == nil {
			return nil
		}
		data, err := json.Marshal(tracks)
		if err != nil {
			return err
		}
		return b.Put([]byte("search:"+query), data)
	})
}

// LoadSearchResults retrieves cached results for query. Returns false when
// there is no cached entry.
func (s *SecretsStore) LoadSearchResults(query string, target any) (bool, error) {
	if s.db == nil {
		return false, nil
	}
	var found bool
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("Cache"))
		if b == nil {
			return nil
		}
		v := b.Get([]byte("search:" + query))
		if v == nil {
			return nil
		}
		found = true
		return json.Unmarshal(v, target)
	})
	return found, err
}

func (s *SecretsStore) Close() {
	if s.db != nil {
		_ = s.db.Close()
	}
}
