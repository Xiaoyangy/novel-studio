package stylestat

import (
	"math"
	"sort"
	"strings"
)

// Task 063：书级 AI 味统计——AI 长篇的典型暴露面在书级（跨章口头禅累积、
// 开头/结尾结构同构、全员声口趋同），章内规则看不见这一层。

// ChapterText 书级统计输入：一章的正文。
type ChapterText struct {
	Chapter int
	Text    string
}

// BookStats 书级统计结果（warning 级信号源，不阻断）。
type BookStats struct {
	PetPhrases         []PetPhrase `json:"pet_phrases,omitempty"` // 跨章重复 n-gram
	OpeningHomogeneity float64     `json:"opening_homogeneity"`   // 章首结构相似度 [0,1]
	EndingHomogeneity  float64     `json:"ending_homogeneity"`    // 章尾结构相似度 [0,1]
	Chapters           int         `json:"chapters"`
}

// PetPhrase 跨章高频短语（叙述层口头禅候选；角色 typical_moves 白名单由调用方过滤）。
type PetPhrase struct {
	Phrase   string `json:"phrase"`
	Chapters int    `json:"chapters"` // 出现的章数
	Total    int    `json:"total"`    // 总次数
}

// BookReport 计算书级统计。petMinChapters：n-gram 至少跨多少章才算口头禅（默认 3）。
func BookReport(chapters []ChapterText, petMinChapters int) BookStats {
	if petMinChapters <= 0 {
		petMinChapters = 3
	}
	stats := BookStats{Chapters: len(chapters)}
	if len(chapters) < 2 {
		return stats
	}

	// 跨章 pet-phrase：4-8 字 n-gram 在 ≥K 章出现。
	perChapter := make([]map[string]int, len(chapters))
	for i, ch := range chapters {
		perChapter[i] = charNgrams(ch.Text, 4, 8)
	}
	agg := map[string][2]int{} // phrase -> [章数, 总次数]
	for _, m := range perChapter {
		for p, c := range m {
			v := agg[p]
			agg[p] = [2]int{v[0] + 1, v[1] + c}
		}
	}
	for p, v := range agg {
		if v[0] >= petMinChapters && v[1] >= v[0]+2 {
			stats.PetPhrases = append(stats.PetPhrases, PetPhrase{Phrase: p, Chapters: v[0], Total: v[1]})
		}
	}
	sort.Slice(stats.PetPhrases, func(i, j int) bool { return stats.PetPhrases[i].Total > stats.PetPhrases[j].Total })
	if len(stats.PetPhrases) > 20 {
		stats.PetPhrases = stats.PetPhrases[:20]
	}

	// 章首/章尾结构同质：首段/末段的模式向量（长度、对白开场、问句收尾、省略号收尾）余弦相似均值。
	var openVecs, endVecs [][]float64
	for _, ch := range chapters {
		paras := splitParas(ch.Text)
		if len(paras) == 0 {
			continue
		}
		openVecs = append(openVecs, paraVector(paras[0]))
		endVecs = append(endVecs, paraVector(paras[len(paras)-1]))
	}
	stats.OpeningHomogeneity = meanPairwiseCosine(openVecs)
	stats.EndingHomogeneity = meanPairwiseCosine(endVecs)
	return stats
}

func charNgrams(text string, minN, maxN int) map[string]int {
	runes := []rune(strings.Join(strings.Fields(text), ""))
	out := map[string]int{}
	for n := minN; n <= maxN; n += 2 {
		for i := 0; i+n <= len(runes); i++ {
			g := string(runes[i : i+n])
			if strings.ContainsAny(g, "。！？，、：；\"\"''") {
				continue
			}
			out[g]++
		}
	}
	for g, c := range out {
		if c < 2 {
			delete(out, g)
		}
	}
	return out
}

func splitParas(text string) []string {
	var out []string
	for _, p := range strings.Split(text, "\n") {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func paraVector(p string) []float64 {
	runes := []rune(p)
	v := []float64{float64(len(runes)) / 100, 0, 0, 0}
	if strings.HasPrefix(p, "“") || strings.HasPrefix(p, "\"") {
		v[1] = 1 // 对白开场
	}
	if strings.HasSuffix(p, "？") || strings.HasSuffix(p, "?") {
		v[2] = 1 // 问句收尾
	}
	if strings.HasSuffix(p, "……") || strings.HasSuffix(p, "...") {
		v[3] = 1
	}
	return v
}

func meanPairwiseCosine(vecs [][]float64) float64 {
	if len(vecs) < 2 {
		return 0
	}
	var sum float64
	var n int
	for i := 0; i < len(vecs); i++ {
		for j := i + 1; j < len(vecs); j++ {
			sum += cosine(vecs[i], vecs[j])
			n++
		}
	}
	return sum / float64(n)
}

func cosine(a, b []float64) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

// DriftReport Task 079：风格漂移检测（同质检测的反向）。对比基线章组与近期章组的
// 风格特征距离：句长均值、对白占比、逗号密度、段均长。距离超阈值 = 疑似模型/配置
// 切换或状态污染。返回 [0,1] 归一化距离（0=无漂移）。
func DriftReport(baseline, current []ChapterText) float64 {
	if len(baseline) == 0 || len(current) == 0 {
		return 0
	}
	b, c := styleFeatures(baseline), styleFeatures(current)
	var dist float64
	for i := range b {
		denom := b[i] + c[i]
		if denom == 0 {
			continue
		}
		d := (b[i] - c[i]) / denom // 归一化差
		dist += d * d
	}
	return math.Sqrt(dist / float64(len(b)))
}

// styleFeatures 特征向量：[平均句长, 对白字符占比, 逗号密度, 平均段长]。
func styleFeatures(chapters []ChapterText) [4]float64 {
	var sentLenSum, sentCount, dialogRunes, totalRunes, commas, paraLenSum, paraCount float64
	for _, ch := range chapters {
		runes := []rune(ch.Text)
		totalRunes += float64(len(runes))
		inDialog := false
		sentLen := 0.0
		for _, r := range runes {
			switch r {
			case '“':
				inDialog = true
			case '”':
				inDialog = false
			case '，':
				commas++
			}
			if inDialog {
				dialogRunes++
			}
			sentLen++
			if r == '。' || r == '！' || r == '？' {
				sentLenSum += sentLen
				sentCount++
				sentLen = 0
			}
		}
		for _, p := range splitParas(ch.Text) {
			paraLenSum += float64(len([]rune(p)))
			paraCount++
		}
	}
	var f [4]float64
	if sentCount > 0 {
		f[0] = sentLenSum / sentCount
	}
	if totalRunes > 0 {
		f[1] = dialogRunes / totalRunes
		f[2] = commas / totalRunes * 100
	}
	if paraCount > 0 {
		f[3] = paraLenSum / paraCount
	}
	return f
}
