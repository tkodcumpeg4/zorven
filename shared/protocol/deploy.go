package protocol

// Acik surum: Zorven Apps (uygulama dagitimi) ticari katmandir. Bu dosya yalniz
// protokol uyumlulugu icin iletisim sabitlerini ve en kucuk mesaj bicimlerini tasir;
// dagitim sema ayrintilari ve uygulamasi yoktur (ozel depodaki deploy*.go yerine gecer).
// Acik istemci bu mesajlari alirsa reddeder (client/agent/apps_oss.go).

// FeatureDeploy, Hello/Welcome'da ilan edilen Apps yetenegi. Acik istemci ilan etmez.
const FeatureDeploy = "deploy_v1"

const (
	TypeDeployApply   MessageType = "deploy_apply"
	TypeDeployAction  MessageType = "deploy_action"
	TypeDeployLogsSub MessageType = "deploy_logs_sub"
	TypeDeployStatus  MessageType = "deploy_status"
	TypeDeployEvent   MessageType = "deploy_event"
	TypeDeployLogs    MessageType = "deploy_logs"
)

// DeployApply, sunucudan istemciye dagitim istegi (yalniz kimlikler).
type DeployApply struct {
	Type         MessageType `json:"type"`
	AppID        string      `json:"app_id"`
	DeploymentID string      `json:"deployment_id"`
}

// DeployAction, sunucudan istemciye eylem istegi (yalniz kimlikler).
type DeployAction struct {
	Type         MessageType `json:"type"`
	AppID        string      `json:"app_id"`
	DeploymentID string      `json:"deployment_id"`
}

// DeployLogsSub, uygulama gunlugu aboneligi.
type DeployLogsSub struct {
	Type  MessageType `json:"type"`
	AppID string      `json:"app_id"`
}
