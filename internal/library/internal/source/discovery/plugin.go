package discovery

import (
	"cmp"
	"context"
	"encoding/json"
	"path"
	"slices"
	"strings"
)

func pluginSearchPaths(ctx context.Context, view View, root string) ([]string, map[string]string, error) {
	var dirs []string
	groups := map[string]string{}
	readManifest := func(name string, dest any) (bool, error) {
		entries, err := readLinkedDir(view, root, path.Join(root, ".claude-plugin"))
		if err != nil {
			return false, nil
		}
		if slices.IndexFunc(entries, func(e Entry) bool { return e.Name == name && e.Mode.IsRegular() }) < 0 {
			return false, nil
		}
		content, err := view.ReadFile(ctx, path.Join(root, ".claude-plugin", name))
		if err != nil {
			return false, err
		}
		if json.Unmarshal(content, dest) != nil {
			return false, nil
		}
		return true, nil
	}
	add := func(base, name string, skills []string) {
		for _, value := range skills {
			dir, ok := pluginLocalPath(root, base, value)
			if !ok {
				continue
			}
			dirs = append(dirs, path.Dir(dir))
			if name != "" {
				if _, exists := groups[dir]; !exists {
					groups[dir] = name
				}
			}
		}
		dirs = append(dirs, path.Join(base, "skills"))
	}
	var market struct {
		Metadata struct {
			PluginRoot string `json:"pluginRoot"`
		} `json:"metadata"`
		Plugins []json.RawMessage `json:"plugins"`
	}
	if _, err := readManifest("marketplace.json", &market); err != nil {
		return nil, nil, err
	}
	if base, ok := pluginLocalPath(root, root, cmp.Or(market.Metadata.PluginRoot, "./")); ok {
		for _, raw := range market.Plugins {
			var plugin struct {
				Name   string   `json:"name"`
				Source string   `json:"source"`
				Skills []string `json:"skills"`
			}
			if json.Unmarshal(raw, &plugin) != nil {
				continue
			}
			if dir, ok := pluginLocalPath(root, base, plugin.Source); ok {
				add(dir, plugin.Name, plugin.Skills)
			}
		}
	}
	var single struct {
		Name   string   `json:"name"`
		Skills []string `json:"skills"`
	}
	found, err := readManifest("plugin.json", &single)
	if err != nil {
		return nil, nil, err
	}
	if found {
		add(root, single.Name, single.Skills)
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
	if !underRoot(dir, root) {
		return "", false
	}
	return dir, true
}
