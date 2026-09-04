package service

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"golang.org/x/sync/errgroup"

	. "github.com/onsi/gomega"
	"github.com/robertkrimen/otto"
	"github.com/samber/lo"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"

	"github.com/Ursa-Minor-Beta/baas/internal/messagebus"
	"github.com/Ursa-Minor-Beta/baas/internal/sessions"
	"github.com/Ursa-Minor-Beta/baas/internal/tempfiles"
	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

// requireChrome skips the test unless a Chrome or Chromium binary is reachable.
// BROWSER_EXECUTABLE wins when set, matching how the service resolves it.
func requireChrome(t *testing.T) {
	t.Helper()
	if path := os.Getenv("BROWSER_EXECUTABLE"); path != "" {
		if _, err := os.Stat(path); err == nil {
			return
		}
		t.Skipf("BROWSER_EXECUTABLE=%q does not exist", path)
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		if _, err := exec.LookPath(name); err == nil {
			return
		}
	}
	t.Skip("no Chrome/Chromium binary found; install one or set BROWSER_EXECUTABLE")
}

// requireDocker skips the test unless a Docker daemon is reachable, which
// testcontainers needs to bring up MongoDB.
func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not found; needed to start the MongoDB test container")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon is not reachable; needed to start the MongoDB test container")
	}
}

// requireBinary skips the test unless name is on PATH.
func requireBinary(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not found on PATH; it ships in the BaaS container image", name)
	}
}

func TestGoogleSearch(t *testing.T) {
	RegisterTestingT(t)
	if os.Getenv("GITHUB_RUN_ID") != "" {
		t.Skip("hits google.com live; not intended to run on CI")
	}
	requireChrome(t)

	b := testSyncBrowser()

	res, err := b.Run(context.TODO(), dto.BrowserOpts{
		ReturnScreenshot: lo.ToPtr(true),
		Secrets: map[string]string{
			"query": "what is LLM?",
		},
		Program: `
navigate('https://google.com'); 
waitReady('body'); 
sleep('1s'); 
click('[role="search"] textarea'); 
log('sending query...'); 
var currentUrl = getURL();
log('getURL1:' + currentUrl); 
sendKeys(getSecret('query') + '\n'); 
sleep('1s'); 
waitReady('body'); 
sleep('1s'); 
currentUrl = getURL();
log('getURL2:' + currentUrl); 
logURL(); 
outerHtml('body') 
`,
	})
	Expect(err).To(BeNil())
	Expect(res.Cookies).NotTo(BeEmpty())
	Expect(res.URL).NotTo(BeEmpty())
	Expect(res.OutHTML).NotTo(BeEmpty())
	Expect(res.Screenshot).NotTo(BeEmpty())
	Expect(res.Log).NotTo(BeEmpty())
	Expect(res.Log).To(ContainElement(MatchRegexp("getURL1:.+")))
	Expect(res.Log).To(ContainElement(MatchRegexp("getURL2:.+")))
	Expect(res.Log).To(ContainElement(MatchRegexp("URL:.+")))
}

func TestAsyncBrowser(t *testing.T) {
	RegisterTestingT(t)
	requireDocker(t)
	requireChrome(t)
	if os.Getenv("GITHUB_RUN_ID") != "" {
		t.Skipf("Not intended to run on CI")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	b, _, _, teardown := testAsyncBrowser(ctx)
	defer teardown()
	sessionID := lo.RandomString(5, lo.LowerCaseLettersCharset)

	var resp *dto.BrowserResponse
	errG := errgroup.Group{}
	errG.Go(func() error {
		time.Sleep(1 * time.Second)
		asyncResp, err := b.RunAsync(ctx, sessionID, dto.BrowserOpts{
			Timeout:          "20s",
			ReturnScreenshot: lo.ToPtr(true),
		}, nil)
		resp = asyncResp
		return err
	})

	time.Sleep(1 * time.Second)

	msgOut, err := b.DoAsyncAndWait(ctx, dto.BrowserMessageIn{
		SessionID: sessionID,
		Timeout:   "15s",
		Program:   "navigate('https://google.com'); 'DONE';",
	})
	Expect(err).To(BeNil())
	Expect(msgOut.Value).To(Equal("DONE"))

	msgOut, err = b.DoAsyncAndWait(ctx, dto.BrowserMessageIn{
		SessionID: sessionID,
		Timeout:   "15s",
		Program:   "sleep('1s'); navigate('https://yandex.ru'); 'DONE';",
	})
	Expect(err).To(BeNil())
	Expect(msgOut.Value).To(Equal("DONE"))

	Expect(errG.Wait())
	Expect(resp).NotTo(BeNil())
	Expect(resp.Screenshot).NotTo(BeEmpty())
}

func startMongodb() (string, *mongo.Database, func(), error) {
	ctx := context.Background()
	mongodbContainer, err := mongodb.Run(ctx, "mongo:6", mongodb.WithReplicaSet("test"))
	if err != nil {
		return "", nil, nil, err
	}
	connStr, err := mongodbContainer.ConnectionString(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	urlObj, err := url.Parse(connStr)
	if err != nil {
		return "", nil, nil, err
	}

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(connStr))
	if err != nil {
		return "", nil, nil, err
	}

	db := client.Database(defaultDatabaseName)

	urlObj.Path = "/" + lo.RandomString(5, lo.LowerCaseLettersCharset)
	return urlObj.String(), db, func() {
		_ = mongodbContainer.Terminate(ctx)
	}, err
}

func testAsyncBrowser(ctx context.Context) (Browser, messagebus.Broker[dto.BrowserMessageIn], messagebus.Broker[dto.BrowserMessageOut], func()) {
	connString, db, teardown, err := startMongodb()
	Expect(err).To(BeNil())
	inMessages, err := messagebus.NewMessageBus[dto.BrowserMessageIn](ctx, logger.NewLogger(), connString, messagesInCollection)
	Expect(err).To(BeNil())
	outMessages, err := messagebus.NewMessageBus[dto.BrowserMessageOut](ctx, logger.NewLogger(), connString, messagesOutCollection)
	Expect(err).To(BeNil())

	log := logger.NewLogger()

	registry := sessions.NewRegistry(log, db, tempfiles.NewNoopManager())
	return NewBrowser(log, "", nil, nil, inMessages, outMessages, registry), inMessages, outMessages, teardown
}

func testSyncBrowser() *browser {
	log := logger.NewLogger()
	return NewBrowser(log, "", nil, nil, nil, nil, sessions.NewNoopRegistry()).(*browser)
}

func Test_browser_llmExtractSelectorFromResponse(t *testing.T) {
	RegisterTestingT(t)
	// A backticked selector keeps its quoted attribute values.
	Expect(llmExtractSingleSelectorFromResponse("`#userNameInput[name=\"UserName\"]`")).
		To(Equal("#userNameInput[name=\"UserName\"]"))
	// A bare quoted selector is unwrapped.
	Expect(llmExtractSingleSelectorFromResponse("The selector is \"#login_email\".")).
		To(Equal("#login_email"))
	Expect(llmExtractSingleSelectorFromResponse("#selector1\nOR\n#selector2")).
		To(Equal("#selector1"))
	Expect(llmExtractSingleSelectorFromResponse("#j_id0:j_ id1:loginForm:username, #j_ id0:j_ id1:loginForm:username")).
		To(Equal("#j_id0:j_ id1:loginForm:username"))
}

func Test_sanitizeHtmlElementId(t *testing.T) {
	RegisterTestingT(t)
	// Disallowed characters collapse to "_", the value is lower-cased, every
	// "id" substring is dropped (it confuses the model), and a random salt is
	// appended so repeated values stay distinguishable.
	Expect(generateAttributeValueWithSalt("")).To(Equal(""))

	got := generateAttributeValueWithSalt("j_id0:j_id1:loginForm:username")
	Expect(got).To(MatchRegexp(`^j_0_j_1_loginform_username-[a-z]{5}$`))

	// Two calls on the same input differ only by salt.
	Expect(generateAttributeValueWithSalt("j_id0:j_id1:loginForm:username")).
		NotTo(Equal(got))
}

func Test_stripAllHtmlTagsExcept(t *testing.T) {
	RegisterTestingT(t)
	rawHTML := `
<div class="login-card" data-llm-id="ymygrmhrao">
	<svg viewBox="0 0 24 24" class="logo" data-llm-id="niourtjvqf">
		<path d="M12 2 L22 22 L2 22 Z"></path>
	</svg>
	<form action="/authenticate" method="post" data-llm-id="pebtnzaemf">
		<div class="field" data-llm-id="azxmnfzmvv">Email Address
			<input id="login_email" name="email" type="text" value="" data-llm-id="opdeiydbrd">
		</div>
		<div class="field" data-llm-id="lwkvumhrlg">Password
			<input id="login_pass" name="password" type="password" data-llm-id="fmjwqxnogj">
		</div>
	</form>
	<script type="text/javascript" data-llm-id="atfbipgirs">trackLogin("beacon");</script>
	<img src="https://cdn.example.com/logo.png" alt="logo" data-llm-id="mgrwakszic">
</div>
`
	filtered := stripAllHtmlTagsExcept(context.Background(), rawHTML, setAttr{name: "data-llm-id"}, "div")

	// disallowed tags are dropped, including the inline script payload
	for _, tag := range []string{"<svg", "<path", "<form", "<input", "<img", "<script"} {
		Expect(filtered).NotTo(ContainSubstring(tag))
	}
	Expect(filtered).NotTo(ContainSubstring("trackLogin"))

	// allowed tags survive, and so do the attributes the LLM prompt relies on
	Expect(filtered).To(ContainSubstring("<div"))
	Expect(filtered).To(ContainSubstring(`data-llm-id="ymygrmhrao"`))
	Expect(filtered).To(ContainSubstring(`class="login-card"`))
	Expect(filtered).To(ContainSubstring("Email Address"))
}

func TestOttoEval(t *testing.T) {
	RegisterTestingT(t)

	vm := otto.New()

	_, err := vm.Eval("var a = 'Hello';")
	Expect(err).To(BeNil())
	v, err := vm.Eval("a + ', World!'")
	Expect(err).To(BeNil())

	Expect(v.String()).NotTo(BeEmpty())
	Expect(v.String()).To(Equal("Hello, World!"))
}

func TestMergeGenerationInfo(t *testing.T) {
	RegisterTestingT(t)
	b := testSyncBrowser()

	withAllValues := map[string]any{
		"CompletionTokens": 1,
		"PromptTokens":     2,
		"TotalTokens":      3,
	}
	withSomeValues := map[string]any{
		"CompletionTokens": 1,
		"TotalTokens":      3,
	}
	empty := map[string]any{}

	Expect(b.mergeGenerationInfo(empty, empty)).To(Equal(empty))

	Expect(b.mergeGenerationInfo(withAllValues, empty)).To(Equal(withAllValues))

	Expect(b.mergeGenerationInfo(withAllValues, withAllValues)).To(Equal(map[string]any{
		"CompletionTokens": 2,
		"PromptTokens":     4,
		"TotalTokens":      6,
	}))

	Expect(b.mergeGenerationInfo(withAllValues, nil)).To(Equal(map[string]any{
		"CompletionTokens": 1,
		"PromptTokens":     2,
		"TotalTokens":      3,
	}))

	Expect(b.mergeGenerationInfo(nil, withAllValues)).To(Equal(map[string]any{
		"CompletionTokens": 1,
		"PromptTokens":     2,
		"TotalTokens":      3,
	}))

	Expect(b.mergeGenerationInfo(withAllValues, withSomeValues)).To(Equal(map[string]any{
		"CompletionTokens": 2,
		"PromptTokens":     2,
		"TotalTokens":      6,
	}))
}
