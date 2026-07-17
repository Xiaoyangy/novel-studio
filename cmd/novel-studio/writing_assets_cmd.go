package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func hasWritingAssetsFlag(argv []string) bool {
	for _, a := range argv {
		if a == "--writing-assets" {
			return true
		}
	}
	return false
}

func parseWritingAssetsFlags(argv []string) ([]string, error) {
	fs := flag.NewFlagSet("writing-assets", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: novel-studio --writing-assets [list|seed-defaults|enable <id>|disable <id>|preset <id> <name> <feature_id...>|bind <scope> ... <id>|trial [scope ...] [brief]|compile]\n\n")
		fmt.Fprintf(os.Stderr, "维护当前项目 output/novel/meta/writing_assets.json 写法资产。\n")
		fmt.Fprintf(os.Stderr, "绑定范围：book <id> | volume <volume> <id> | arc <volume> <arc> <id> | chapter <chapter> <id> | trial <id>\n")
	}
	if err := fs.Parse(argv); err != nil {
		return nil, err
	}
	return fs.Args(), nil
}

func writingAssetsPipeline(opts cliOptions, args []string) error {
	if hasHelpToken(args) {
		_, _ = parseWritingAssetsFlags([]string{"--help"})
		return nil
	}
	extra, err := parseWritingAssetsFlags(args)
	if err != nil {
		return err
	}
	cfg, _, err := loadCfgBundle(opts)
	if err != nil {
		return err
	}
	st := store.NewStore(cfg.OutputDir)
	action := "list"
	if len(extra) > 0 {
		action = extra[0]
	}
	if action != "list" {
		if err := requireWritingAssetsMutationAllowed(st, action); err != nil {
			return err
		}
	}
	switch action {
	case "list":
		if len(extra) > 1 {
			return fmt.Errorf("list 不接受额外参数：%v", extra[1:])
		}
		return printWritingAssets(st)
	case "seed-defaults":
		if len(extra) > 1 {
			return fmt.Errorf("seed-defaults 不接受额外参数：%v", extra[1:])
		}
		return seedDefaultWritingAssets(st)
	case "enable", "disable":
		if len(extra) != 2 {
			return fmt.Errorf("%s 需要 feature id", action)
		}
		return setWritingFeatureEnabled(st, extra[1], action == "enable")
	case "preset":
		if len(extra) < 4 {
			return fmt.Errorf("preset 需要 id、name 和至少一个 feature id")
		}
		return upsertWritingPreset(st, extra[1], extra[2], extra[3:])
	case "bind":
		if len(extra) < 3 {
			return fmt.Errorf("bind 需要 scope 和 feature/preset id")
		}
		return bindWritingAsset(st, extra[1:])
	case "trial":
		return createWritingTrial(st, extra[1:])
	case "compile":
		if len(extra) > 1 {
			return fmt.Errorf("compile 不接受额外参数：%v", extra[1:])
		}
		return compileWritingAssets(st)
	default:
		return fmt.Errorf("unknown writing-assets action %q", action)
	}
}

func requireWritingAssetsMutationAllowed(st *store.Store, action string) error {
	if st == nil {
		return fmt.Errorf("writing-assets %s requires store", action)
	}
	if lock, err := st.Runtime.LoadPipelineExecution(); err != nil {
		return err
	} else if lock != nil {
		return fmt.Errorf(
			"writing-assets %s 不能在 execution lock 期间改写渲染依赖（mode=%s owner=%s）",
			action,
			lock.Mode,
			lock.Owner,
		)
	}
	mode, err := st.LoadWritingPipelineMode()
	if err != nil || mode == nil || mode.Mode != domain.WritingPipelineModeSealedTwoPassV2 {
		return err
	}
	projected := st.ProjectedV2()
	if active, err := projected.LoadActiveGeneration(); err != nil {
		return err
	} else if active != nil {
		return fmt.Errorf(
			"writing-assets %s 会改写 active sealed generation %s 的渲染依赖；请在新一轮 project-all --restart 之前完成写法资产调整",
			action,
			active.GenerationID,
		)
	}
	if cursor, err := projected.LoadProjectionCursor(); err != nil {
		return err
	} else if cursor != nil {
		return fmt.Errorf(
			"writing-assets %s 会改写正在构建或已封存 generation %s 的规划依赖；请先显式 --restart",
			action,
			cursor.GenerationID,
		)
	}
	return nil
}

func seedDefaultWritingAssets(st *store.Store) error {
	features, presets, bindings, err := st.WritingAssets.SeedDefaults()
	if err != nil {
		return err
	}
	fmt.Printf("seeded default writing assets: features=%d presets=%d bindings=%d\n", features, presets, bindings)
	return nil
}

func printWritingAssets(st *store.Store) error {
	lib, err := st.WritingAssets.Load()
	if err != nil {
		return err
	}
	if lib == nil || len(lib.Features) == 0 {
		fmt.Println("writing assets: empty")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tENABLED\tCATEGORY\tNAME\tSOURCE")
	for _, f := range lib.Features {
		fmt.Fprintf(tw, "%s\t%t\t%s\t%s\t%s\n", f.ID, f.Enabled, f.Category, f.Name, f.Source)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if len(lib.Presets) > 0 {
		fmt.Println("\nPresets:")
		for _, p := range lib.Presets {
			fmt.Printf("- %s\t%s\t%s\n", p.ID, p.Name, strings.Join(p.FeatureIDs, ","))
		}
	}
	if len(lib.Bindings) > 0 {
		fmt.Println("\nBindings:")
		for _, b := range lib.Bindings {
			target := b.FeatureID
			if b.PresetID != "" {
				target = b.PresetID
			}
			fmt.Printf("- %s -> %s\n", writingAssetBindingLabel(b), target)
		}
	}
	return nil
}

func setWritingFeatureEnabled(st *store.Store, id string, enabled bool) error {
	lib, err := st.WritingAssets.Load()
	if err != nil {
		return err
	}
	if lib == nil {
		return fmt.Errorf("writing assets empty")
	}
	found := false
	for i := range lib.Features {
		if lib.Features[i].ID == id {
			lib.Features[i].Enabled = enabled
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("feature %q not found", id)
	}
	if err := st.WritingAssets.Save(*lib); err != nil {
		return err
	}
	fmt.Printf("feature %s enabled=%t\n", id, enabled)
	return nil
}

func compileWritingAssets(st *store.Store) error {
	lib, err := st.WritingAssets.Load()
	if err != nil {
		return err
	}
	if lib == nil {
		return fmt.Errorf("writing assets empty")
	}
	if err := st.WritingAssets.Save(*lib); err != nil {
		return err
	}
	fmt.Printf("compiled %d writing features\n", len(lib.Features))
	return nil
}

func upsertWritingPreset(st *store.Store, id, name string, featureIDs []string) error {
	lib, err := st.WritingAssets.Load()
	if err != nil {
		return err
	}
	if lib == nil {
		return fmt.Errorf("writing assets empty")
	}
	known := make(map[string]struct{}, len(lib.Features))
	for _, f := range lib.Features {
		known[f.ID] = struct{}{}
	}
	for _, id := range featureIDs {
		if _, ok := known[id]; !ok {
			return fmt.Errorf("feature %q not found", id)
		}
	}
	next := domain.WritingPreset{ID: id, Name: name, FeatureIDs: uniqueWritingAssetIDs(featureIDs)}
	found := false
	for i := range lib.Presets {
		if lib.Presets[i].ID == id {
			lib.Presets[i] = next
			found = true
			break
		}
	}
	if !found {
		lib.Presets = append(lib.Presets, next)
	}
	if err := st.WritingAssets.Save(*lib); err != nil {
		return err
	}
	fmt.Printf("preset %s saved with %d features\n", id, len(next.FeatureIDs))
	return nil
}

func bindWritingAsset(st *store.Store, args []string) error {
	lib, err := st.WritingAssets.Load()
	if err != nil {
		return err
	}
	if lib == nil {
		return fmt.Errorf("writing assets empty")
	}
	binding, targetID, err := parseWritingAssetBinding(args)
	if err != nil {
		return err
	}
	if isWritingPresetID(*lib, targetID) {
		binding.PresetID = targetID
	} else if isWritingFeatureID(*lib, targetID) {
		binding.FeatureID = targetID
	} else {
		return fmt.Errorf("feature/preset %q not found", targetID)
	}
	lib.Bindings = upsertWritingBinding(lib.Bindings, binding)
	if err := st.WritingAssets.Save(*lib); err != nil {
		return err
	}
	fmt.Printf("bound %s to %s\n", targetID, writingAssetBindingLabel(binding))
	return nil
}

func createWritingTrial(st *store.Store, args []string) error {
	scope, brief, err := parseWritingAssetTrial(args)
	if err != nil {
		return err
	}
	compiled, err := st.WritingAssets.CompileForScope(8, &scope)
	if err != nil {
		return err
	}
	if compiled == nil {
		return fmt.Errorf("writing assets empty")
	}
	rel, err := st.WritingAssets.SaveTrial(scope, brief, *compiled)
	if err != nil {
		return err
	}
	fmt.Printf("writing trial saved: %s\n", rel)
	return nil
}

func parseWritingAssetBinding(args []string) (domain.WritingBinding, string, error) {
	scope := args[0]
	switch scope {
	case "book":
		if len(args) != 2 {
			return domain.WritingBinding{}, "", fmt.Errorf("bind book 需要 id")
		}
		return domain.WritingBinding{Scope: "book"}, args[1], nil
	case "volume":
		if len(args) != 3 {
			return domain.WritingBinding{}, "", fmt.Errorf("bind volume 需要 volume 和 id")
		}
		vol, err := strconv.Atoi(args[1])
		if err != nil || vol <= 0 {
			return domain.WritingBinding{}, "", fmt.Errorf("invalid volume %q", args[1])
		}
		return domain.WritingBinding{Scope: "volume", Volume: vol}, args[2], nil
	case "arc":
		if len(args) != 4 {
			return domain.WritingBinding{}, "", fmt.Errorf("bind arc 需要 volume、arc 和 id")
		}
		vol, err := strconv.Atoi(args[1])
		if err != nil || vol <= 0 {
			return domain.WritingBinding{}, "", fmt.Errorf("invalid volume %q", args[1])
		}
		arc, err := strconv.Atoi(args[2])
		if err != nil || arc <= 0 {
			return domain.WritingBinding{}, "", fmt.Errorf("invalid arc %q", args[2])
		}
		return domain.WritingBinding{Scope: "arc", Volume: vol, Arc: arc}, args[3], nil
	case "chapter":
		if len(args) != 3 {
			return domain.WritingBinding{}, "", fmt.Errorf("bind chapter 需要 chapter 和 id")
		}
		chapter, err := strconv.Atoi(args[1])
		if err != nil || chapter <= 0 {
			return domain.WritingBinding{}, "", fmt.Errorf("invalid chapter %q", args[1])
		}
		return domain.WritingBinding{Scope: "chapter", Chapter: chapter}, args[2], nil
	case "trial":
		if len(args) != 2 {
			return domain.WritingBinding{}, "", fmt.Errorf("bind trial 需要 id")
		}
		return domain.WritingBinding{Scope: "trial"}, args[1], nil
	default:
		return domain.WritingBinding{}, "", fmt.Errorf("unknown binding scope %q", scope)
	}
}

func parseWritingAssetTrial(args []string) (domain.WritingBinding, string, error) {
	if len(args) == 0 {
		return domain.WritingBinding{Scope: "trial"}, "", nil
	}
	switch args[0] {
	case "book":
		return domain.WritingBinding{Scope: "book"}, strings.Join(args[1:], " "), nil
	case "volume":
		if len(args) < 2 {
			return domain.WritingBinding{}, "", fmt.Errorf("trial volume 需要 volume")
		}
		vol, err := strconv.Atoi(args[1])
		if err != nil || vol <= 0 {
			return domain.WritingBinding{}, "", fmt.Errorf("invalid volume %q", args[1])
		}
		return domain.WritingBinding{Scope: "volume", Volume: vol}, strings.Join(args[2:], " "), nil
	case "arc":
		if len(args) < 3 {
			return domain.WritingBinding{}, "", fmt.Errorf("trial arc 需要 volume 和 arc")
		}
		vol, err := strconv.Atoi(args[1])
		if err != nil || vol <= 0 {
			return domain.WritingBinding{}, "", fmt.Errorf("invalid volume %q", args[1])
		}
		arc, err := strconv.Atoi(args[2])
		if err != nil || arc <= 0 {
			return domain.WritingBinding{}, "", fmt.Errorf("invalid arc %q", args[2])
		}
		return domain.WritingBinding{Scope: "arc", Volume: vol, Arc: arc}, strings.Join(args[3:], " "), nil
	case "chapter":
		if len(args) < 2 {
			return domain.WritingBinding{}, "", fmt.Errorf("trial chapter 需要 chapter")
		}
		chapter, err := strconv.Atoi(args[1])
		if err != nil || chapter <= 0 {
			return domain.WritingBinding{}, "", fmt.Errorf("invalid chapter %q", args[1])
		}
		return domain.WritingBinding{Scope: "chapter", Chapter: chapter}, strings.Join(args[2:], " "), nil
	case "trial":
		return domain.WritingBinding{Scope: "trial"}, strings.Join(args[1:], " "), nil
	default:
		return domain.WritingBinding{Scope: "trial"}, strings.Join(args, " "), nil
	}
}

func upsertWritingBinding(bindings []domain.WritingBinding, next domain.WritingBinding) []domain.WritingBinding {
	for i := range bindings {
		if bindings[i] == next {
			return bindings
		}
	}
	return append(bindings, next)
}

func isWritingFeatureID(lib domain.WritingAssetLibrary, id string) bool {
	for _, f := range lib.Features {
		if f.ID == id {
			return true
		}
	}
	return false
}

func isWritingPresetID(lib domain.WritingAssetLibrary, id string) bool {
	for _, p := range lib.Presets {
		if p.ID == id {
			return true
		}
	}
	return false
}

func uniqueWritingAssetIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	var out []string
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func writingAssetBindingLabel(binding domain.WritingBinding) string {
	switch binding.Scope {
	case "volume":
		return fmt.Sprintf("volume:%d", binding.Volume)
	case "arc":
		return fmt.Sprintf("volume:%d/arc:%d", binding.Volume, binding.Arc)
	case "chapter":
		return fmt.Sprintf("chapter:%d", binding.Chapter)
	case "trial":
		return "trial"
	default:
		return "book"
	}
}
