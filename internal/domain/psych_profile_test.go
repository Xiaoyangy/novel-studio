package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBigFiveValidate(t *testing.T) {
	ok := BigFive{Openness: 0.7, Conscientiousness: 0.5, Extraversion: 0, Agreeableness: 1, Neuroticism: 0.3}
	if err := ok.Validate(); err != nil {
		t.Fatalf("合法值不应报错: %v", err)
	}
	for _, bad := range []BigFive{
		{Openness: -0.1},
		{Neuroticism: 1.1},
	} {
		if err := bad.Validate(); err == nil {
			t.Fatalf("越界值应报错: %+v", bad)
		}
	}
}

func TestBigFiveGenerateProfile(t *testing.T) {
	b := BigFive{Openness: 0.8, Conscientiousness: 0.5, Extraversion: 0.5, Agreeableness: 0.2, Neuroticism: 0.9}
	got := b.GenerateProfile()
	for _, want := range []string{"高O", "低A", "高N"} {
		if !strings.Contains(got, want) {
			t.Fatalf("简档 %q 应包含 %q", got, want)
		}
	}
	if strings.Contains(got, "C") || strings.Contains(got, "E") {
		t.Fatalf("中间分不应出现在简档: %q", got)
	}
	balanced := BigFive{Openness: 0.5, Conscientiousness: 0.5, Extraversion: 0.5, Agreeableness: 0.5, Neuroticism: 0.5}
	if balanced.GenerateProfile() != "五维均衡" {
		t.Fatalf("均衡分应返回固定文案，实际 %q", balanced.GenerateProfile())
	}
}

func TestBigFiveExpectedEmotionRange(t *testing.T) {
	low, high := BigFive{Neuroticism: 0}.ExpectedEmotionRange()
	if low != 0 || high != 0.5 {
		t.Fatalf("N=0 期望 [0,0.5]，实际 [%v,%v]", low, high)
	}
	low, high = BigFive{Neuroticism: 1}.ExpectedEmotionRange()
	if low != 0.5 || high != 1 {
		t.Fatalf("N=1 期望 [0.5,1]，实际 [%v,%v]", low, high)
	}
}

func TestAttachmentStyleValidate(t *testing.T) {
	for _, s := range []AttachmentStyle{AttachmentSecure, AttachmentAnxiousPreoccupied, AttachmentDismissiveAvoidant, AttachmentFearfulAvoidant} {
		if err := s.Validate(); err != nil {
			t.Fatalf("合法依恋类型 %s 不应报错: %v", s, err)
		}
	}
	if err := AttachmentStyle("clingy").Validate(); err == nil {
		t.Fatal("非法依恋类型应报错")
	}
}

func TestEmotionVectorValidateAndDeriveLabel(t *testing.T) {
	if err := (EmotionVector{Valence: 1.2}).Validate(); err == nil {
		t.Fatal("valence 越界应报错")
	}
	if err := (EmotionVector{Arousal: -1.5}).Validate(); err == nil {
		t.Fatal("arousal 越界应报错")
	}
	cases := []struct {
		v    EmotionVector
		want string
	}{
		{EmotionVector{Valence: 0.8, Arousal: 0.8, Intensity: 0.9}, "狂喜"},
		{EmotionVector{Valence: 0.5, Arousal: -0.5, Intensity: 0.9}, "满足"},
		{EmotionVector{Valence: -0.5, Arousal: 0.5, Intensity: 0.9}, "愤怒"},
		{EmotionVector{Valence: -0.5, Arousal: -0.5, Intensity: 0.9}, "抑郁"},
		{EmotionVector{Valence: -0.5, Arousal: -0.5, Intensity: 0.2}, "低落"},
	}
	for _, c := range cases {
		if err := c.v.Validate(); err != nil {
			t.Fatalf("合法值不应报错: %v", err)
		}
		if got := c.v.DeriveLabel(); got != c.want {
			t.Fatalf("(%v,%v,%v) 期望 %q，实际 %q", c.v.Valence, c.v.Arousal, c.v.Intensity, c.want, got)
		}
	}
}

func TestCognitiveBiasProfileValidate(t *testing.T) {
	if len(AllBiasTypes) != 15 {
		t.Fatalf("应有 15 种偏差类型，实际 %d", len(AllBiasTypes))
	}
	for _, bt := range AllBiasTypes {
		if err := bt.Validate(); err != nil {
			t.Fatalf("合法偏差 %s 不应报错: %v", bt, err)
		}
	}
	if err := BiasType("astrology").Validate(); err == nil {
		t.Fatal("非法偏差类型应报错")
	}
	bad := CognitiveBiasProfile{Biases: []BiasActivation{{Type: BiasAnchoring, Intensity: 1.5}}}
	if err := bad.Validate(); err == nil {
		t.Fatal("强度越界应报错")
	}
}

func TestSchwartzAndMoralFoundationsAndCHCValidate(t *testing.T) {
	if err := (SchwartzValues{Benevolence: 0.9, Power: 0.1}).Validate(); err != nil {
		t.Fatalf("合法价值观不应报错: %v", err)
	}
	if err := (SchwartzValues{Hedonism: 2}).Validate(); err == nil {
		t.Fatal("价值观越界应报错")
	}
	if err := (MoralFoundations{HarmCare: 0.8}).Validate(); err != nil {
		t.Fatalf("合法道德基础不应报错: %v", err)
	}
	if err := (MoralFoundations{LoyaltyBetrayal: -0.2}).Validate(); err == nil {
		t.Fatal("道德基础越界应报错")
	}
	if err := (CHCAbilities{Knowledge: 0.9, FluidReasoning: 0.2}).Validate(); err != nil {
		t.Fatalf("合法能力矩阵不应报错: %v", err)
	}
	if err := (CHCAbilities{ProcessingSpeed: 1.01}).Validate(); err == nil {
		t.Fatal("能力矩阵越界应报错")
	}
}

func TestCharacterDNAIsEmpty(t *testing.T) {
	if !(CharacterDNA{}).IsEmpty() {
		t.Fatal("空 DNA 应为 empty")
	}
	if (CharacterDNA{Hidden: []string{"旧伤"}}).IsEmpty() {
		t.Fatal("有内容的 DNA 不应为 empty")
	}
}

func TestNormalizeResourceKind(t *testing.T) {
	cases := map[string]ResourceKind{
		"currency": ResourceCurrency,
		"secret":   ResourceSecret,
		"asset":    ResourceCurrency,
		"item":     ResourceCurrency,
		"skill":    ResourceAbility,
		"place":    ResourceRelations,
		"debt":     ResourceCredit,
		"":         ResourceOther,
		"whatever": ResourceOther,
	}
	for raw, want := range cases {
		if got := NormalizeResourceKind(raw); got != want {
			t.Fatalf("NormalizeResourceKind(%q) 期望 %s，实际 %s", raw, want, got)
		}
	}
}

func TestMoralCeilingIsEmpty(t *testing.T) {
	if !(MoralCeiling{}).IsEmpty() {
		t.Fatal("零值天花板应为 empty")
	}
	if (MoralCeiling{TabooZones: []string{"儿童"}}).IsEmpty() {
		t.Fatal("有禁区的天花板不应为 empty")
	}
}

func TestCharacterPsychBackwardCompat(t *testing.T) {
	// 老数据（无 psych 字段）反序列化不报错且 Psych 为 nil。
	old := `{"name":"林昭","role":"主角","description":"...","arc":"...","traits":["坚韧"]}`
	var c Character
	if err := json.Unmarshal([]byte(old), &c); err != nil {
		t.Fatalf("老 Character JSON 读取失败: %v", err)
	}
	if c.Psych != nil {
		t.Fatal("老数据 Psych 应为 nil")
	}
	// 无 Psych 时序列化不产生 psych 键。
	out, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "psych") {
		t.Fatalf("无画像时不应序列化 psych 键: %s", out)
	}
	// 带 Psych 往返无损。
	c.Psych = &CharacterPsychProfile{
		BigFive: &BigFive{Openness: 0.8, Neuroticism: 0.7},
		DNA:     &CharacterDNA{Exposed: []string{"左脸疤"}, Latent: []string{"皇室血脉"}},
	}
	if err := c.Psych.Validate(); err != nil {
		t.Fatalf("合法画像不应报错: %v", err)
	}
	round, _ := json.Marshal(c)
	var back Character
	if err := json.Unmarshal(round, &back); err != nil {
		t.Fatalf("往返失败: %v", err)
	}
	if back.Psych == nil || back.Psych.BigFive == nil || back.Psych.BigFive.Openness != 0.8 {
		t.Fatal("Psych 往返丢失")
	}
	if back.Psych.DNA == nil || len(back.Psych.DNA.Latent) != 1 {
		t.Fatal("DNA 往返丢失")
	}
}

func TestCharacterPsychProfileValidateAggregation(t *testing.T) {
	p := &CharacterPsychProfile{BigFive: &BigFive{Openness: 1.5}}
	if err := p.Validate(); err == nil {
		t.Fatal("子维度越界应向上冒泡")
	}
	var nilP *CharacterPsychProfile
	if err := nilP.Validate(); err != nil {
		t.Fatal("nil 画像应视为合法")
	}
}

func TestCharacterPsychProfileUnmarshalTolerant(t *testing.T) {
	t.Run("形状不符的子维度单独降级其余保留", func(t *testing.T) {
		// values / moral_foundations 数组形状来自 MiniMax 实跑故障现场。
		src := `{
			"big_five": {"openness":0.6,"conscientiousness":0.9,"extraversion":0.3,"agreeableness":0.4,"neuroticism":0.5},
			"values": ["安全","成就","权力"],
			"moral_foundations": [{"name":"公平","score":0.8}]
		}`
		var p CharacterPsychProfile
		if err := json.Unmarshal([]byte(src), &p); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if p.BigFive == nil || p.BigFive.Conscientiousness != 0.9 {
			t.Fatalf("big_five 应保留，got %+v", p.BigFive)
		}
		if p.Values != nil || p.MoralFoundations != nil {
			t.Fatalf("形状不符的维度应为 nil，got values=%+v mf=%+v", p.Values, p.MoralFoundations)
		}
		if len(p.DegradedDims) != 2 || p.DegradedDims[0] != "values" || p.DegradedDims[1] != "moral_foundations" {
			t.Fatalf("degraded_dims 应记录 [values moral_foundations]，got %v", p.DegradedDims)
		}
	})

	t.Run("psych 本体非对象时整体降级不报错", func(t *testing.T) {
		var p CharacterPsychProfile
		if err := json.Unmarshal([]byte(`["果断","冷静"]`), &p); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(p.DegradedDims) != 1 || p.DegradedDims[0] != "psych" {
			t.Fatalf("整体降级应记录 psych，got %v", p.DegradedDims)
		}
	})

	t.Run("null 与合法对象照常工作", func(t *testing.T) {
		var p CharacterPsychProfile
		if err := json.Unmarshal([]byte(`null`), &p); err != nil {
			t.Fatalf("null: %v", err)
		}
		if len(p.DegradedDims) != 0 {
			t.Fatalf("null 不应降级，got %v", p.DegradedDims)
		}
		src := `{"values":{"values":{"self_direction":0.7,"stimulation":0.2,"hedonism":0.1,"achievement":0.8,"power":0.6,"security":0.9,"tradition":0.3,"conformity":0.4,"benevolence":0.5,"universalism":0.2},"primary_driver":"security + achievement"}}`
		if err := json.Unmarshal([]byte(src), &p); err != nil {
			t.Fatalf("object: %v", err)
		}
		if p.Values == nil || p.Values.Values.Security != 0.9 {
			t.Fatalf("合法 values 应解析，got %+v", p.Values)
		}
		if err := p.Validate(); err != nil {
			t.Fatalf("validate: %v", err)
		}
	})
}
