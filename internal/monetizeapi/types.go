package monetizeapi

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	Group   = "obol.org"
	Version = "v1alpha1"

	ServiceOfferKind        = "ServiceOffer"
	RegistrationRequestKind = "RegistrationRequest"

	ServiceOfferResource        = "serviceoffers"
	RegistrationRequestResource = "registrationrequests"

	PausedAnnotation = "obol.org/paused"
)

var (
	ServiceOfferGVR        = schema.GroupVersionResource{Group: Group, Version: Version, Resource: ServiceOfferResource}
	RegistrationRequestGVR = schema.GroupVersionResource{Group: Group, Version: Version, Resource: RegistrationRequestResource}

	ServiceGVR    = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}
	SecretGVR     = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}
	ConfigMapGVR  = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
	DeploymentGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	MiddlewareGVR = schema.GroupVersionResource{Group: "traefik.io", Version: "v1alpha1", Resource: "middlewares"}
	HTTPRouteGVR  = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}
)

type ServiceOffer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ServiceOfferSpec   `json:"spec,omitempty"`
	Status            ServiceOfferStatus `json:"status,omitempty"`
}

type ServiceOfferSpec struct {
	Type         string                   `json:"type,omitempty"`
	Model        ServiceOfferModel        `json:"model,omitempty"`
	Upstream     ServiceOfferUpstream     `json:"upstream,omitempty"`
	Payment      ServiceOfferPayment      `json:"payment,omitempty"`
	Path         string                   `json:"path,omitempty"`
	Provenance   map[string]string        `json:"provenance,omitempty"`
	Registration ServiceOfferRegistration `json:"registration,omitempty"`
}

type ServiceOfferModel struct {
	Name    string `json:"name,omitempty"`
	Runtime string `json:"runtime,omitempty"`
}

type ServiceOfferUpstream struct {
	Service    string `json:"service,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	Port       int64  `json:"port,omitempty"`
	HealthPath string `json:"healthPath,omitempty"`
}

type ServiceOfferPayment struct {
	Scheme            string                 `json:"scheme,omitempty"`
	Network           string                 `json:"network,omitempty"`
	PayTo             string                 `json:"payTo,omitempty"`
	MaxTimeoutSeconds int64                  `json:"maxTimeoutSeconds,omitempty"`
	Price             ServiceOfferPriceTable `json:"price,omitempty"`
}

type ServiceOfferPriceTable struct {
	PerRequest string `json:"perRequest,omitempty"`
	PerMTok    string `json:"perMTok,omitempty"`
	PerHour    string `json:"perHour,omitempty"`
	PerEpoch   string `json:"perEpoch,omitempty"`
}

type ServiceOfferRegistration struct {
	Enabled        bool                  `json:"enabled,omitempty"`
	Name           string                `json:"name,omitempty"`
	Description    string                `json:"description,omitempty"`
	Image          string                `json:"image,omitempty"`
	Services       []ServiceOfferService `json:"services,omitempty"`
	SupportedTrust []string              `json:"supportedTrust,omitempty"`
	Skills         []string              `json:"skills,omitempty"`
	Domains        []string              `json:"domains,omitempty"`
	Metadata       map[string]string     `json:"metadata,omitempty"`
}

type ServiceOfferService struct {
	Name     string `json:"name,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	Version  string `json:"version,omitempty"`
}

type ServiceOfferStatus struct {
	Conditions         []Condition `json:"conditions,omitempty"`
	Endpoint           string      `json:"endpoint,omitempty"`
	AgentID            string      `json:"agentId,omitempty"`
	RegistrationTxHash string      `json:"registrationTxHash,omitempty"`
	ObservedGeneration int64       `json:"observedGeneration,omitempty"`
}

type Condition struct {
	Type               string      `json:"type"`
	Status             string      `json:"status"`
	Reason             string      `json:"reason,omitempty"`
	Message            string      `json:"message,omitempty"`
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
}

type RegistrationRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              RegistrationRequestSpec   `json:"spec,omitempty"`
	Status            RegistrationRequestStatus `json:"status,omitempty"`
}

type RegistrationRequestSpec struct {
	ServiceOfferName      string `json:"serviceOfferName,omitempty"`
	ServiceOfferNamespace string `json:"serviceOfferNamespace,omitempty"`
	DesiredState          string `json:"desiredState,omitempty"`
}

type RegistrationRequestStatus struct {
	Phase              string `json:"phase,omitempty"`
	Message            string `json:"message,omitempty"`
	PublishedURL       string `json:"publishedUrl,omitempty"`
	AgentID            string `json:"agentId,omitempty"`
	RegistrationTxHash string `json:"registrationTxHash,omitempty"`
}

func (o *ServiceOffer) EffectiveNamespace() string {
	if o.Spec.Upstream.Namespace != "" {
		return o.Spec.Upstream.Namespace
	}
	return o.Namespace
}

func (o *ServiceOffer) EffectivePort() int64 {
	if o.Spec.Upstream.Port > 0 {
		return o.Spec.Upstream.Port
	}
	return 11434
}

func (o *ServiceOffer) EffectiveHealthPath() string {
	if o.Spec.Upstream.HealthPath != "" {
		return o.Spec.Upstream.HealthPath
	}
	return "/"
}

func (o *ServiceOffer) EffectivePath() string {
	if o.Spec.Path != "" {
		return o.Spec.Path
	}
	return fmt.Sprintf("/services/%s", o.Name)
}

func (o *ServiceOffer) IsInference() bool {
	return o.Spec.Type == "" || o.Spec.Type == "inference"
}

func (o *ServiceOffer) IsPaused() bool {
	return o.Annotations != nil && o.Annotations[PausedAnnotation] == "true"
}
