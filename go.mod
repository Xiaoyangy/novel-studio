module github.com/chenhongyang/novel-studio

go 1.25.5

require (
	github.com/voocel/agentcore v1.7.7
	golang.org/x/text v0.38.0
)

require (
	github.com/voocel/litellm v1.8.3 // indirect
	golang.org/x/image v0.43.0 // indirect
)

replace github.com/voocel/litellm => ./third_party/litellm
