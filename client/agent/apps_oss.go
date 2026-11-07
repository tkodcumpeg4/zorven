package agent

import (
	"context"
	"net/http"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Acik surum: Zorven Apps (Docker ile uygulama calistirma) ticari katmandir. Bu dosya,
// acik ajanin geri kalani derlensin diye en kucuk yer tutuculari tasir: uygulama
// yoneticisi hic kurulmaz (a.apps hep nil), zapp:// hedefleri cozulmez ve gelen
// dagitim mesajlari reddedilir. Ozel depodaki apps*.go dosyalarinin yerini alir.

const (
	zappScheme      = "zapp://"
	zappUnavailable = "zapp-unavailable"
)

type zappTable struct{}

func newZappTable() *zappTable { return &zappTable{} }

func zappBase(*zappTable, string) string { return zappUnavailable }

type appLogs struct{}

func (appLogs) handle(context.Context, protocol.DeployLogsSub) {}

// AppManager, acik surumde bos yer tutucudur (EnableDeploy etkisizdir).
type AppManager struct{ logs appLogs }

func newAppManager(any, *zappTable) *AppManager                        { return &AppManager{} }
func (*AppManager) Run(context.Context)                                {}
func (*AppManager) Check(context.Context) error                        { return nil }
func (*AppManager) Available() bool                                    { return false }
func (*AppManager) attachSender(func(any, protocol.MessageType) error) {}
func (*AppManager) detachSender()                                      {}
func (*AppManager) Apply(context.Context, protocol.DeployApply)        {}
func (*AppManager) Action(context.Context, protocol.DeployAction)      {}

func (a *Agent) rejectDeployDisabled(context.Context, *clientSession, string, string) {}

func (cs *clientSession) respondAppUnavailable(ctx context.Context, req protocol.HTTPRequest, start time.Time) {
	if cs.onRequest != nil {
		cs.onRequest(req.Method, req.Path, http.StatusServiceUnavailable, time.Since(start))
	}
}
