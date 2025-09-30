package tsparams

import (
	"github.com/openshift-kni/k8sreporter"
	moduleV1Beta1 "github.com/rh-ecosystem-edge/eco-goinfra/pkg/schemes/kmm/v1beta1"
	olmv1alpha1 "github.com/rh-ecosystem-edge/eco-goinfra/pkg/schemes/olm/operators/v1alpha1"
	"github.com/rh-ecosystem-edge/eco-gotests/tests/hw-accel/kmm/internal/kmmparams"
	corev1 "k8s.io/api/core/v1"
)

var (
	// Labels represents the range of labels that can be used for test cases selection.
	Labels = append(kmmparams.Labels, LabelSuite)

	// ReporterNamespacesToDump tells to the reporter from where to collect logs.
	ReporterNamespacesToDump = map[string]string{
		kmmparams.KmmOperatorNamespace: "op",
		"kmm-upgrade-test":             "upgrade-test",
	}

	// ReporterCRDsToDump tells to the reporter what CRs to dump.
	ReporterCRDsToDump = []k8sreporter.CRData{
		{Cr: &moduleV1Beta1.ModuleList{}},
		{Cr: &moduleV1Beta1.ModuleBuildSignConfigList{}},
		{Cr: &moduleV1Beta1.NodeModulesConfigList{}},
		{Cr: &olmv1alpha1.ClusterServiceVersionList{}},
		{Cr: &olmv1alpha1.InstallPlanList{}},
		{Cr: &corev1.EventList{}},
	}
)
