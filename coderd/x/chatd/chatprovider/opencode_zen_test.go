package chatprovider_test

import (
	"testing"

	"charm.land/fantasy/providers/openaicompat"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/x/chatd/chatprovider"
	"github.com/coder/coder/v2/codersdk"
)

func TestValidateOpenCodeZenModel(t *testing.T) {
	t.Parallel()

	const zenBaseURL = "https://opencode.ai/zen/go/v1"

	tests := []struct {
		name       string
		baseURL    string
		model      string
		wantErr    bool
		errContain string
	}{
		{
			name:    "allows free model",
			baseURL: zenBaseURL,
			model:   "muse-spark-1.3-contributor-free",
		},
		{
			name:    "rejects paid model",
			baseURL: zenBaseURL,
			model:   "muse-spark-1.3",
			wantErr: true,
			errContain: "allowed free models: big-pickle, mimo-v2.5-free, " +
				"ling-3.0-flash-fin-free, nemotron-3-ultra-free, " +
				"nemotron-3.5-lightning-free, muse-spark-1.3-contributor-free",
		},
		{
			name:    "does not restrict other providers",
			baseURL: "https://example.com/v1",
			model:   "muse-spark-1.3",
		},
		{
			name:    "does not match lookalike host",
			baseURL: "https://not-opencode.ai/zen/v1",
			model:   "muse-spark-1.3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := chatprovider.ValidateOpenCodeZenModel(tt.baseURL, tt.model)
			if tt.wantErr {
				require.ErrorContains(t, err, "paid OpenCode Zen model")
				require.ErrorContains(t, err, tt.errContain)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestModelFromConfigRejectsPaidOpenCodeZenModel(t *testing.T) {
	t.Parallel()

	model, err := chatprovider.ModelFromConfig(
		openaicompat.Name,
		"muse-spark-1.3",
		chatprovider.ProviderAPIKeys{
			ByProvider: map[string]string{openaicompat.Name: "test-key"},
			BaseURLByProvider: map[string]string{
				openaicompat.Name: "https://opencode.ai/zen/go/v1",
			},
		},
		chatprovider.UserAgent(),
		nil,
		nil,
		&codersdk.ChatModelOpenAIConfig{UseResponsesAPI: new(true)},
	)

	require.False(t, model.Valid())
	require.ErrorContains(t, err, "paid OpenCode Zen model \"muse-spark-1.3\" is disabled")
	require.ErrorContains(t, err, "muse-spark-1.3-contributor-free")
}
