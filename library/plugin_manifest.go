package library

import (
	"context"
	"encoding/json"
	"path"
	"strings"
)

// pluginSearchPaths 只读取 Claude 清单的 Skill 路径与展示分组,不引入持久化实体。
func pluginSearchPaths(ctx context.Context, view sourceDirectoryView, root string) ([]string, map[string]string, error) {
	var dirs []string
	groups := map[string]string{}
	readManifest := func(name string, dest any) error {
		entries, err := view.readDir(root)
		if err != nil {
			return nil
		}
		realDir := false
		for _, e := range entries {
			if e.name == ".claude-plugin" && e.mode.IsDir() {
				realDir = true
				break
			}
		}
		if !realDir {
			return nil
		}
		entries, err = view.readDir(path.Join(root, ".claude-plugin"))
		if err != nil {
			return nil
		}
		for _, e := range entries {
			if e.name != name || !e.mode.IsRegular() {
				continue
			}
			content, err := view.readFile(ctx, path.Join(root, ".claude-plugin", name))
			if err != nil {
				return err
			}
			// JSON 无效时静默忽略整个清单,不使用部分解码的数据。
			if !json.Valid(content) {
				return nil
			}
			_ = json.Unmarshal(content, dest)
			return nil
		}
		return nil
	}
	add := func(base string, raw json.RawMessage) {
		var plugin struct {
			Name   string `json:"name"`
			Skills []any  `json:"skills"`
		}
		if json.Unmarshal(raw, &plugin) != nil {
			return
		}
		for _, value := range plugin.Skills {
			value, ok := value.(string)
			if !ok {
				continue
			}
			dir, ok := pluginLocalPath(root, base, value)
			if !ok {
				continue
			}
			dirs = append(dirs, path.Dir(dir))
			if plugin.Name != "" {
				if _, exists := groups[dir]; !exists {
					groups[dir] = plugin.Name
				}
			}
		}
		dirs = append(dirs, path.Join(base, "skills"))
	}
	var marketplace json.RawMessage
	if err := readManifest("marketplace.json", &marketplace); err != nil {
		return nil, nil, err
	}
	var market struct {
		Metadata struct {
			PluginRoot *string `json:"pluginRoot"`
		} `json:"metadata"`
		Plugins []json.RawMessage `json:"plugins"`
	}
	if len(marketplace) != 0 && json.Unmarshal(marketplace, &market) == nil {
		pluginRoot := "./"
		if market.Metadata.PluginRoot != nil {
			pluginRoot = *market.Metadata.PluginRoot
		}
		base, ok := pluginLocalPath(root, root, pluginRoot)
		if ok {
			for _, raw := range market.Plugins {
				var plugin struct {
					Source string `json:"source"`
				}
				if json.Unmarshal(raw, &plugin) != nil {
					continue
				}
				dir, ok := pluginLocalPath(root, base, plugin.Source)
				if ok {
					add(dir, raw)
				}
			}
		}
	}
	var single json.RawMessage
	if err := readManifest("plugin.json", &single); err != nil {
		return nil, nil, err
	}
	if len(single) != 0 {
		add(root, single)
	}
	return dirs, groups, nil
}

func pluginLocalPath(root, base, value string) (string, bool) {
	if !strings.HasPrefix(value, "./") || strings.ContainsAny(value, "\\\x00") {
		return "", false
	}
	for part := range strings.SplitSeq(value, "/") {
		if part == ".." {
			return "", false
		}
	}
	dir := path.Join(base, value)
	if !githubUnderRoot(dir, root) || dir == ".." || strings.HasPrefix(dir, "../") {
		return "", false
	}
	return dir, true
}
