package wkdb

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
)

func (wk *wukongDB) AddOrUpdatePlugin(plugin Plugin) error {

	db := wk.defaultShardDB()
	batch := db.NewBatch()
	defer batch.Close()

	if err := wk.writePlugin(plugin, batch); err != nil {
		return err
	}
	return batch.Commit(wk.sync)
}

func (wk *wukongDB) DeletePlugin(no string) error {
	db := wk.defaultShardDB()
	batch := db.NewBatch()
	defer batch.Close()

	id := key.HashWithString(no)
	if err := batch.DeleteRange(key.NewPluginColumnKey(id, key.MinColumnKey), key.NewPluginColumnKey(id, key.MaxColumnKey), wk.noSync); err != nil {
		return err
	}

	return batch.Commit(wk.sync)
}

func (wk *wukongDB) GetPlugins() ([]Plugin, error) {
	db := wk.defaultShardDB()
	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewPluginColumnKey(0, key.MinColumnKey),
		UpperBound: key.NewPluginColumnKey(math.MaxUint64, key.MaxColumnKey),
	})
	defer iter.Close()

	plugins := make([]Plugin, 0)
	if err := wk.iteratorPlugin(iter, func(u Plugin) bool {
		plugins = append(plugins, u)
		return true
	}); err != nil {
		return nil, err
	}
	return plugins, nil
}

func (wk *wukongDB) GetPlugin(no string) (Plugin, error) {
	db := wk.defaultShardDB()
	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewPluginColumnKey(key.HashWithString(no), key.MinColumnKey),
		UpperBound: key.NewPluginColumnKey(key.HashWithString(no), key.MaxColumnKey),
	})
	defer iter.Close()

	var plugin Plugin
	if err := wk.iteratorPlugin(iter, func(u Plugin) bool {
		plugin = u
		return false
	}); err != nil {
		return Plugin{}, err
	}
	return plugin, nil
}

func (wk *wukongDB) UpdatePluginConfig(no string, config map[string]interface{}) error {
	db := wk.defaultShardDB()
	batch := db.NewBatch()
	defer batch.Close()

	id := key.HashWithString(no)
	configBytes, err := json.Marshal(config)
	if err != nil {
		return err
	}

	if err := batch.Set(key.NewPluginColumnKey(id, key.TablePlugin.Column.Config), configBytes, wk.noSync); err != nil {
		return err
	}

	return batch.Commit(wk.sync)
}

func (wk *wukongDB) GetPluginConfig(no string) (map[string]interface{}, error) {
	db := wk.defaultShardDB()
	data, closer, err := db.Get(key.NewPluginColumnKey(key.HashWithString(no), key.TablePlugin.Column.Config))
	if closer != nil {
		defer closer.Close()
	}
	if err != nil {
		return nil, err
	}

	config := make(map[string]interface{})
	if len(data) > 0 {
		if err := json.Unmarshal(data, &config); err != nil {
			return nil, err
		}
	}
	return config, nil
}

func (wk *wukongDB) writePlugin(plugin Plugin, w pebble.Writer) error {

	id := key.HashWithString(plugin.No)
	var err error

	// no
	if err = w.Set(key.NewPluginColumnKey(id, key.TablePlugin.Column.No), []byte(plugin.No), wk.noSync); err != nil {
		return err
	}

	// name
	if err = w.Set(key.NewPluginColumnKey(id, key.TablePlugin.Column.Name), []byte(plugin.Name), wk.noSync); err != nil {
		return err
	}

	// configTemplate
	if err = w.Set(key.NewPluginColumnKey(id, key.TablePlugin.Column.ConfigTemplate), plugin.ConfigTemplate, wk.noSync); err != nil {
		return err
	}

	// createAt
	if plugin.CreatedAt != nil {
		createdAtBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(createdAtBytes, uint64(plugin.CreatedAt.UnixNano()))
		if err = w.Set(key.NewPluginColumnKey(id, key.TablePlugin.Column.CreatedAt), createdAtBytes, wk.noSync); err != nil {
			return err
		}
	}

	// updateAt
	if plugin.UpdatedAt != nil {
		updatedAtBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(updatedAtBytes, uint64(plugin.UpdatedAt.UnixNano()))
		if err = w.Set(key.NewPluginColumnKey(id, key.TablePlugin.Column.UpdatedAt), updatedAtBytes, wk.noSync); err != nil {
			return err
		}
	}

	// status
	statusBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(statusBytes, uint32(plugin.Status))
	if err = w.Set(key.NewPluginColumnKey(id, key.TablePlugin.Column.Status), statusBytes, wk.noSync); err != nil {
		return err
	}

	// version
	if err = w.Set(key.NewPluginColumnKey(id, key.TablePlugin.Column.Version), []byte(plugin.Version), wk.noSync); err != nil {
		return err
	}

	// methods
	var methodBytes []byte
	if len(plugin.Methods) > 0 {
		methodBytes, err = json.Marshal(plugin.Methods)
		if err != nil {
			return err
		}
	}
	if err = w.Set(key.NewPluginColumnKey(id, key.TablePlugin.Column.Methods), methodBytes, wk.noSync); err != nil {
		return err
	}

	// priority
	priorityBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(priorityBytes, plugin.Priority)
	if err = w.Set(key.NewPluginColumnKey(id, key.TablePlugin.Column.Priority), priorityBytes, wk.noSync); err != nil {
		return err
	}

	// config
	var configBytes []byte
	if len(plugin.Config) > 0 {
		configBytes, err = json.Marshal(plugin.Config)
		if err != nil {
			return err
		}
	}
	if err = w.Set(key.NewPluginColumnKey(id, key.TablePlugin.Column.Config), configBytes, wk.noSync); err != nil {
		return err
	}

	return nil
}

func (wk *wukongDB) iteratorPlugin(iter *pebble.Iterator, iterFnc func(u Plugin) bool) error {
	var (
		preId          uint64
		prePlugin      Plugin
		lastNeedAppend bool = true
		hasData        bool = false
	)

	for iter.First(); iter.Valid(); iter.Next() {
		primaryKey, columnName, err := key.ParsePluginColumnKey(iter.Key())
		if err != nil {
			return err
		}

		if preId != primaryKey {
			if preId != 0 {
				if !iterFnc(prePlugin) {
					lastNeedAppend = false
					break
				}
			}
			preId = primaryKey
			prePlugin = Plugin{}
		}

		switch columnName {
		case key.TablePlugin.Column.No:
			prePlugin.No = string(iter.Value())
		case key.TablePlugin.Column.Name:
			prePlugin.Name = string(iter.Value())
		case key.TablePlugin.Column.ConfigTemplate:
			cfgTemplate := make([]byte, len(iter.Value()))
			copy(cfgTemplate, iter.Value())
			prePlugin.ConfigTemplate = cfgTemplate
		case key.TablePlugin.Column.Status:
			prePlugin.Status = PluginStatus(wk.endian.Uint32(iter.Value()))
		case key.TablePlugin.Column.Version:
			prePlugin.Version = string(iter.Value())
		case key.TablePlugin.Column.Methods:
			if len(iter.Value()) > 0 {
				if err := json.Unmarshal(iter.Value(), &prePlugin.Methods); err != nil {
					return err
				}
			}
		case key.TablePlugin.Column.Priority:
			prePlugin.Priority = wk.endian.Uint32(iter.Value())
		case key.TablePlugin.Column.CreatedAt:
			tm := int64(wk.endian.Uint64(iter.Value()))
			if tm > 0 {
				t := time.Unix(tm/1e9, tm%1e9)
				prePlugin.CreatedAt = &t
			}

		case key.TablePlugin.Column.UpdatedAt:
			tm := int64(wk.endian.Uint64(iter.Value()))
			if tm > 0 {
				t := time.Unix(tm/1e9, tm%1e9)
				prePlugin.UpdatedAt = &t
			}
		case key.TablePlugin.Column.Config:
			if len(iter.Value()) > 0 {
				if err := json.Unmarshal(iter.Value(), &prePlugin.Config); err != nil {
					return err
				}
			}
		}
		lastNeedAppend = true
		hasData = true
	}

	if lastNeedAppend && hasData {
		_ = iterFnc(prePlugin)
	}
	return nil
}
