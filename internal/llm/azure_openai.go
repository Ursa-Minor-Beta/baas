package llm

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/azure"
	"github.com/openai/openai-go/option"
	"github.com/pkg/errors"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"
)

const DefaultAPIVersion = "2024-08-01-preview"

type azureOpenAIClient struct {
	log    logger.Logger
	client *openai.Client
	model  string
}

func NewAzureOpenAI(log logger.Logger, endpoint, apiKey, model, apiVersion string) (Client, error) {
	if apiVersion == "" {
		apiVersion = DefaultAPIVersion
	}

	var client openai.Client

	if apiKey != "" {
		client = openai.NewClient(
			option.WithAPIKey(apiKey),
			azure.WithEndpoint(endpoint, apiVersion),
		)
	} else {
		tokenCredential, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create default Azure credential")
		}

		client = openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(tokenCredential),
		)
	}

	return &azureOpenAIClient{
		log:    log,
		client: &client,
		model:  model,
	}, nil
}

func (a *azureOpenAIClient) Generate(ctx context.Context, request GenerateRequest) (*GenerateResponse, error) {
	resp, err := a.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{{
			OfUser: &openai.ChatCompletionUserMessageParam{
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfString: openai.String(request.Prompt),
				},
			},
		}},
		MaxTokens:   openai.Int(1024),  // You might want to make this configurable
		Temperature: openai.Float(0.0), // You might want to make this configurable
		Model:       a.model,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate content")
	}

	if len(resp.Choices) == 0 {
		return nil, errors.New("response does not contain any result")
	}

	genInfo := map[string]any{
		"model":           resp.Model,
		"completion_id":   resp.ID,
		"completion_type": "chat.completion",
		"usage":           resp.Usage,
	}

	a.log.Infof(a.log.WithValue(ctx, "generationInfo", genInfo), "got response from Azure OpenAI")

	return &GenerateResponse{
		Response:       resp.Choices[0].Message.Content,
		GenerationInfo: genInfo,
	}, nil
}
