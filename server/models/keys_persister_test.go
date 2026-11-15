package models

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/meshery/meshkit/database"
	meshkiterrors "github.com/meshery/meshkit/errors"
	"github.com/meshery/schemas/models/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestKeyPersisterDB(t *testing.T) (*database.Handler, func()) {
	t.Helper()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test_keys.db")

	dbHandler, err := database.New(database.Options{
		Engine:   database.SQLITE,
		Filename: dbPath,
	})
	require.NoError(t, err)

	err = dbHandler.AutoMigrate(&Key{})
	require.NoError(t, err)

	cleanup := func() {
		// In meshkit, database.Handler wraps gorm.DB, sqlite closes with connection pool
		sqlDB, err := dbHandler.DB.DB()
		if err == nil && sqlDB != nil {
			_ = sqlDB.Close()
		}
	}

	return &dbHandler, cleanup
}

func TestKeyPersister_NilReceiverAndDBGuards(t *testing.T) {
	var nilPersister *KeyPersister
	persisterWithNilDB := &KeyPersister{DB: nil}

	randomID := core.Uuid(uuid.Must(uuid.NewV4()))
	testKey := &Key{ID: randomID}

	t.Run("nil persister receiver returns ErrDBConnection", func(t *testing.T) {
		_, err := nilPersister.GetUsersKeys("", "", "")
		assert.ErrorIs(t, err, ErrDBConnection)

		_, err = nilPersister.GetUsersKey(randomID)
		assert.ErrorIs(t, err, ErrDBConnection)

		_, err = nilPersister.SaveUsersKey(testKey)
		assert.ErrorIs(t, err, ErrDBConnection)

		_, err = nilPersister.SaveUsersKeys([]Key{*testKey})
		assert.ErrorIs(t, err, ErrDBConnection)
	})

	t.Run("persister with nil DB returns ErrDBConnection", func(t *testing.T) {
		_, err := persisterWithNilDB.GetUsersKeys("", "", "")
		assert.ErrorIs(t, err, ErrDBConnection)

		_, err = persisterWithNilDB.GetUsersKey(randomID)
		assert.ErrorIs(t, err, ErrDBConnection)

		_, err = persisterWithNilDB.SaveUsersKey(testKey)
		assert.ErrorIs(t, err, ErrDBConnection)

		_, err = persisterWithNilDB.SaveUsersKeys([]Key{*testKey})
		assert.ErrorIs(t, err, ErrDBConnection)
	})

	t.Run("SaveUsersKey rejects nil key", func(t *testing.T) {
		dbHandler, cleanup := setupTestKeyPersisterDB(t)
		defer cleanup()

		kp := &KeyPersister{DB: dbHandler}
		_, err := kp.SaveUsersKey(nil)
		require.Error(t, err)
		assert.Equal(t, ErrDBCreateCode, meshkiterrors.GetCode(err))
	})
}

func TestKeyPersister_GetUsersKey_NotFound(t *testing.T) {
	dbHandler, cleanup := setupTestKeyPersisterDB(t)
	defer cleanup()

	kp := &KeyPersister{DB: dbHandler}
	nonExistentID := core.Uuid(uuid.Must(uuid.NewV4()))

	data, err := kp.GetUsersKey(nonExistentID)
	assert.Nil(t, data, "data should be nil when key is not found")
	require.Error(t, err)
	assert.Equal(t, ErrResultNotFoundCode, meshkiterrors.GetCode(err), "should return ErrResultNotFound on missing key")
}

func TestKeyPersister_CRUDOperationsAndFiltering(t *testing.T) {
	dbHandler, cleanup := setupTestKeyPersisterDB(t)
	defer cleanup()

	kp := &KeyPersister{DB: dbHandler}

	id1 := core.Uuid(uuid.Must(uuid.NewV4()))
	id2 := core.Uuid(uuid.Must(uuid.NewV4()))

	key1 := Key{
		ID:          id1,
		Function:    "Encryption",
		Category:    "Security",
		Subcategory: "AES",
		CreatedAt:   time.Now().Add(-2 * time.Hour),
		UpdatedAt:   time.Now().Add(-1 * time.Hour),
	}
	key2 := Key{
		ID:          id2,
		Function:    "Signing",
		Category:    "Auth",
		Subcategory: "RSA",
		CreatedAt:   time.Now().Add(-1 * time.Hour),
		UpdatedAt:   time.Now(),
	}

	// 1. Test SaveUsersKey
	saved1, err := kp.SaveUsersKey(&key1)
	require.NoError(t, err)
	assert.Equal(t, id1, saved1.ID)

	// 2. Test SaveUsersKeys (batch)
	savedList, err := kp.SaveUsersKeys([]Key{key2})
	require.NoError(t, err)
	assert.Len(t, savedList, 1)

	// 3. Test GetUsersKey
	fetchedBytes, err := kp.GetUsersKey(id1)
	require.NoError(t, err)
	var fetchedKey Key
	err = json.Unmarshal(fetchedBytes, &fetchedKey)
	require.NoError(t, err)
	assert.Equal(t, key1.Function, fetchedKey.Function)

	// 4. Test GetUsersKeys with Search
	searchBytes, err := kp.GetUsersKeys("encrypt", "updated_at desc", "")
	require.NoError(t, err)
	var page KeysPage
	err = json.Unmarshal(searchBytes, &page)
	require.NoError(t, err)
	assert.Equal(t, 1, page.TotalCount)
	assert.Len(t, page.Keys, 1)
	assert.Equal(t, "Encryption", page.Keys[0].Function)

	// 5. Test GetUsersKeys without filter
	allBytes, err := kp.GetUsersKeys("", "function asc", "")
	require.NoError(t, err)
	err = json.Unmarshal(allBytes, &page)
	require.NoError(t, err)
	assert.Equal(t, 2, page.TotalCount)
	assert.Len(t, page.Keys, 2)
}

func TestKeyPersister_DBQueryErrorPropagation(t *testing.T) {
	dbHandler, cleanup := setupTestKeyPersisterDB(t)
	kp := &KeyPersister{DB: dbHandler}

	// Close underlying SQL database connection to force query errors
	sqlDB, err := dbHandler.DB.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	defer cleanup()

	t.Run("GetUsersKeys propagates ErrDBRead on query failure", func(t *testing.T) {
		data, err := kp.GetUsersKeys("", "", "")
		assert.Nil(t, data)
		require.Error(t, err)
		assert.Equal(t, ErrDBReadCode, meshkiterrors.GetCode(err))
	})

	t.Run("GetUsersKey propagates ErrDBRead on query failure", func(t *testing.T) {
		data, err := kp.GetUsersKey(core.Uuid(uuid.Must(uuid.NewV4())))
		assert.Nil(t, data)
		require.Error(t, err)
		assert.Equal(t, ErrDBReadCode, meshkiterrors.GetCode(err))
	})

	t.Run("SaveUsersKey propagates ErrDBCreate on save failure", func(t *testing.T) {
		k := &Key{ID: core.Uuid(uuid.Must(uuid.NewV4()))}
		_, err := kp.SaveUsersKey(k)
		require.Error(t, err)
		assert.Equal(t, ErrDBCreateCode, meshkiterrors.GetCode(err))
	})
}
