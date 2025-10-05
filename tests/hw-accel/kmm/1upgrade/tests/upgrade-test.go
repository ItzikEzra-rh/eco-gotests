package tests

import (
	"fmt"
	"strings"
	"time"

	"github.com/golang/glog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rh-ecosystem-edge/eco-goinfra/pkg/configmap"
	"github.com/rh-ecosystem-edge/eco-goinfra/pkg/deployment"
	"github.com/rh-ecosystem-edge/eco-goinfra/pkg/kmm"
	"github.com/rh-ecosystem-edge/eco-goinfra/pkg/namespace"
	"github.com/rh-ecosystem-edge/eco-goinfra/pkg/nodes"
	"github.com/rh-ecosystem-edge/eco-goinfra/pkg/olm"
	"github.com/rh-ecosystem-edge/eco-goinfra/pkg/pod"
	"github.com/rh-ecosystem-edge/eco-goinfra/pkg/reportxml"
	"github.com/rh-ecosystem-edge/eco-goinfra/pkg/serviceaccount"
	"github.com/rh-ecosystem-edge/eco-gotests/tests/hw-accel/kmm/1upgrade/internal/await"
	"github.com/rh-ecosystem-edge/eco-gotests/tests/hw-accel/kmm/1upgrade/internal/tsparams"
	kmmawait "github.com/rh-ecosystem-edge/eco-gotests/tests/hw-accel/kmm/internal/await"
	"github.com/rh-ecosystem-edge/eco-gotests/tests/hw-accel/kmm/internal/check"
	"github.com/rh-ecosystem-edge/eco-gotests/tests/hw-accel/kmm/internal/define"
	. "github.com/rh-ecosystem-edge/eco-gotests/tests/hw-accel/kmm/internal/kmminittools"
	"github.com/rh-ecosystem-edge/eco-gotests/tests/hw-accel/kmm/internal/kmmparams"
	. "github.com/rh-ecosystem-edge/eco-gotests/tests/internal/inittools"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

var _ = Describe("KMM", Ordered, Label(tsparams.LabelSuite), func() {
	Context("Operator", Label("upgrade"), func() {
		const (
			upgradeTestNamespace = "kmm-upgrade-test"
			moduleName           = "simple-kmod-upgrade"
			kmodName             = "simple-kmod"
			serviceAccountName   = "upgrade-test-manager"
		)

		var (
			testImage = fmt.Sprintf("%s/%s/%s:$KERNEL_FULL_VERSION",
				kmmparams.LocalImageRegistry, upgradeTestNamespace, kmodName)
			buildArgValue = fmt.Sprintf("%s.o", kmodName)
		)

		BeforeAll(func() {
			By("Prepare environment for KMM upgrade tests execution")

			By("Create helper ServiceAccount")
			svcAccountBuilder := serviceaccount.NewBuilder(APIClient, "kmm-test-helper", kmmparams.KmmOperatorNamespace)
			svcAccount, err := svcAccountBuilder.Create()
			Expect(err).ToNot(HaveOccurred(), "error creating serviceaccount")

			// If the service account already exists, pull it to get the proper object
			if svcAccount.Object == nil || svcAccount.Object.Name == "" {
				svcAccount, err = serviceaccount.Pull(APIClient, "kmm-test-helper", kmmparams.KmmOperatorNamespace)
				Expect(err).ToNot(HaveOccurred(), "error pulling existing serviceaccount")
			}
			Expect(svcAccount.Object).ToNot(BeNil(), "serviceaccount object should not be nil")
			Expect(svcAccount.Object.Name).To(Equal("kmm-test-helper"), "serviceaccount name should be correct")

			By("Create helper ClusterRoleBinding")
			crb := define.ModuleCRB(*svcAccount, "kmm-test-helper")
			_, err = crb.Create()
			Expect(err).ToNot(HaveOccurred(), "error creating clusterrolebinding")

			By("Create helper Deployments")
			nodeList, err := nodes.List(
				APIClient, metav1.ListOptions{LabelSelector: labels.Set(GeneralConfig.WorkerLabelMap).String()})

			if err != nil {
				Skip(fmt.Sprintf("Error listing worker nodes. Got error: '%v'", err))
			}
			for _, node := range nodeList {
				glog.V(kmmparams.KmmLogLevel).Infof("Creating privileged deployment on node '%v'", node.Object.Name)

				deploymentName := fmt.Sprintf("%s-%s", kmmparams.KmmTestHelperLabelName, node.Object.Name)
				containerCfg, _ := pod.NewContainerBuilder("test", kmmparams.DTKImage,
					[]string{"/bin/bash", "-c", "sleep INF"}).
					WithSecurityContext(kmmparams.PrivilegedSC).GetContainerCfg()

				deploymentCfg := deployment.NewBuilder(APIClient, deploymentName, kmmparams.KmmOperatorNamespace,
					map[string]string{kmmparams.KmmTestHelperLabelName: ""}, *containerCfg)
				deploymentCfg.WithToleration(kmmparams.TolerationNoExecuteK8sUnreachable)
				deploymentCfg.WithToleration(kmmparams.TolerationNoScheduleK8sUnreachable)
				deploymentCfg.WithToleration(kmmparams.TolerationNoScheduleK8sUnschedulable)
				deploymentCfg.WithToleration(kmmparams.TolerationNoScheduleK8sDiskPressure)
				deploymentCfg.WithToleration(kmmparams.TolerationNoExecuteKeyValue)
				deploymentCfg.WithToleration(kmmparams.TolerationNoScheduleKeyValue)

				deploymentCfg.WithLabel(kmmparams.KmmTestHelperLabelName, "").
					WithNodeSelector(map[string]string{"kubernetes.io/hostname": node.Object.Name}).
					WithServiceAccountName("kmm-operator-module-loader")

				_, err = deploymentCfg.CreateAndWaitUntilReady(10 * time.Minute)

				if err != nil {
					Skip(fmt.Sprintf("Could not create deploymentCfg on %s. Got error : %v", node.Object.Name, err))
				}
			}

			By("Create test namespace")
			_, err = namespace.NewBuilder(APIClient, upgradeTestNamespace).Create()
			Expect(err).ToNot(HaveOccurred(), "error creating test namespace")

			// Deploy a simple module to verify upgrade behavior with existing modules
			By("Create ConfigMap for module build")
			configmapContents := define.SimpleKmodConfigMapContents()
			dockerfileConfigMap, err := configmap.
				NewBuilder(APIClient, kmodName, upgradeTestNamespace).
				WithData(configmapContents).Create()
			Expect(err).ToNot(HaveOccurred(), "error creating configmap")

			By("Create ServiceAccount")
			testSvcAccount, err := serviceaccount.
				NewBuilder(APIClient, serviceAccountName, upgradeTestNamespace).Create()
			Expect(err).ToNot(HaveOccurred(), "error creating serviceaccount")

			By("Create ClusterRoleBinding")
			testCrb := define.ModuleCRB(*testSvcAccount, kmodName)
			_, err = testCrb.Create()
			Expect(err).ToNot(HaveOccurred(), "error creating clusterrolebinding")

			By("Create KernelMapping")
			kernelMapping := kmm.NewRegExKernelMappingBuilder("^.+$")
			kernelMapping.WithContainerImage(testImage).
				WithBuildArg(kmmparams.BuildArgName, buildArgValue).
				WithBuildDockerCfgFile(dockerfileConfigMap.Object.Name)
			kerMapOne, err := kernelMapping.BuildKernelMappingConfig()
			Expect(err).ToNot(HaveOccurred(), "error creating kernel mapping")

			By("Create ModuleLoaderContainer")
			moduleLoaderContainer := kmm.NewModLoaderContainerBuilder(kmodName)
			moduleLoaderContainer.WithModprobeSpec("/opt", "", nil, nil, nil, nil)
			moduleLoaderContainer.WithKernelMapping(kerMapOne)
			moduleLoaderContainer.WithImagePullPolicy("Always")
			moduleLoaderContainerCfg, err := moduleLoaderContainer.BuildModuleLoaderContainerCfg()
			Expect(err).ToNot(HaveOccurred(), "error creating moduleloadercontainer")

			By("Create Module")
			module := kmm.NewModuleBuilder(APIClient, moduleName, upgradeTestNamespace).
				WithNodeSelector(GeneralConfig.WorkerLabelMap)
			module = module.WithModuleLoaderContainer(moduleLoaderContainerCfg).
				WithLoadServiceAccount(testSvcAccount.Object.Name)
			_, err = module.Create()
			Expect(err).ToNot(HaveOccurred(), "error creating module")

			By("Await build pod to complete build")
			err = kmmawait.BuildPodCompleted(APIClient, upgradeTestNamespace, 5*time.Minute)
			Expect(err).ToNot(HaveOccurred(), "error while building module")

			By("Await driver container deployment")
			err = kmmawait.ModuleDeployment(APIClient, moduleName, upgradeTestNamespace, time.Minute,
				GeneralConfig.WorkerLabelMap)
			Expect(err).ToNot(HaveOccurred(), "error while waiting on driver deployment")

			By("Check module is loaded on node before upgrade")
			err = check.ModuleLoaded(APIClient, kmodName, time.Minute)
			Expect(err).ToNot(HaveOccurred(), "error while checking the module is loaded")
		})

		AfterAll(func() {
			By("Delete Module")
			_, err := kmm.NewModuleBuilder(APIClient, moduleName, upgradeTestNamespace).Delete()
			if err != nil {
				glog.V(90).Infof("error deleting module: %v", err)
			}

			By("Await module to be deleted")
			err = kmmawait.ModuleObjectDeleted(APIClient, moduleName, upgradeTestNamespace, 3*time.Minute)
			if err != nil {
				glog.V(90).Infof("error while waiting module to be deleted: %v", err)
			}

			By("Await pods deletion")
			err = kmmawait.ModuleUndeployed(APIClient, upgradeTestNamespace, time.Minute)
			if err != nil {
				glog.V(90).Infof("error while waiting pods to be deleted: %v", err)
			}

			svcAccount := serviceaccount.NewBuilder(APIClient, serviceAccountName, upgradeTestNamespace)
			if svcAccount.Exists() {
				By("Delete ClusterRoleBinding")
				crb := define.ModuleCRB(*svcAccount, kmodName)
				err := crb.Delete()
				if err != nil {
					glog.V(90).Infof("error deleting clusterrolebinding: %v", err)
				}

				By("Delete ServiceAccount")
				err = svcAccount.Delete()
				if err != nil {
					glog.V(90).Infof("error deleting serviceaccount: %v", err)
				}
			}

			By("Delete test namespace")
			err = namespace.NewBuilder(APIClient, upgradeTestNamespace).Delete()
			if err != nil {
				glog.V(90).Infof("error deleting test namespace: %v", err)
			}

			By("Cleanup helper deployments")
			testDeployments, err := deployment.List(APIClient, kmmparams.KmmOperatorNamespace, metav1.ListOptions{})
			if err != nil {
				glog.V(kmmparams.KmmLogLevel).Infof("Error listing deployments during cleanup: %v", err)
			} else {
				for _, deploymentObj := range testDeployments {
					glog.V(kmmparams.KmmLogLevel).Infof("Deployment: '%s'\n", deploymentObj.Object.Name)
					if strings.Contains(deploymentObj.Object.Name, kmmparams.KmmTestHelperLabelName) {
						glog.V(kmmparams.KmmLogLevel).Infof("Deleting deployment: '%s'\n", deploymentObj.Object.Name)
						err = deploymentObj.DeleteAndWait(time.Minute)
						if err != nil {
							glog.V(kmmparams.KmmLogLevel).Infof("Error deleting deployment %s: %v", deploymentObj.Object.Name, err)
						}
					}
				}
			}

			By("Cleanup helper ClusterRoleBinding")
			svcAccountBuilder := serviceaccount.NewBuilder(APIClient, "kmm-test-helper", kmmparams.KmmOperatorNamespace)
			if svcAccountBuilder.Exists() {
				svcAccount, err := serviceaccount.Pull(APIClient, "kmm-test-helper", kmmparams.KmmOperatorNamespace)
				if err == nil && svcAccount.Object != nil {
					crb := define.ModuleCRB(*svcAccount, "kmm-test-helper")
					err = crb.Delete()
					if err != nil {
						glog.V(kmmparams.KmmLogLevel).Infof("Error deleting clusterrolebinding: %v", err)
					}
				}
			}

			By("Cleanup helper ServiceAccount")
			svcAccountBuilder = serviceaccount.NewBuilder(APIClient, "kmm-test-helper", kmmparams.KmmOperatorNamespace)
			err = svcAccountBuilder.Delete()
			if err != nil {
				glog.V(kmmparams.KmmLogLevel).Infof("Error deleting serviceaccount: %v", err)
			}
		})

		It("should upgrade successfully with module deployed", reportxml.ID("53609"), func() {
			if ModulesConfig.CatalogSourceName == "" {
				Skip("No CatalogSourceName defined. Skipping test")
			}

			if ModulesConfig.UpgradeTargetVersion == "" {
				Skip("No UpgradeTargetVersion defined. Skipping test ")
			}

			opNamespace := kmmparams.KmmOperatorNamespace
			if strings.Contains(ModulesConfig.SubscriptionName, "hub") {
				opNamespace = kmmparams.KmmHubOperatorNamespace
			}
			By("Getting KMM subscription")
			sub, err := olm.PullSubscription(APIClient, ModulesConfig.SubscriptionName, opNamespace)
			Expect(err).ToNot(HaveOccurred(), "failed getting subscription")

			By("Update subscription to use new channel, if defined")
			if ModulesConfig.CatalogSourceChannel != "" {
				glog.V(90).Infof("setting subscription channel to: %s", ModulesConfig.CatalogSourceChannel)
				sub.Definition.Spec.Channel = ModulesConfig.CatalogSourceChannel
			}

			By("Update subscription to use new catalog source")
			glog.V(90).Infof("Subscription's catalog source: %s", sub.Object.Spec.CatalogSource)
			sub.Definition.Spec.CatalogSource = ModulesConfig.CatalogSourceName
			_, err = sub.Update()
			Expect(err).ToNot(HaveOccurred(), "failed updating subscription")

			By("Approve install plan for upgrade")
			installPlans, err := olm.ListInstallPlan(APIClient, opNamespace)
			Expect(err).ToNot(HaveOccurred(), "failed listing install plans")

			for _, ip := range installPlans {
				if ip.Object.Spec.Approval == "Manual" && !ip.Object.Spec.Approved {
					glog.V(90).Infof("Approving install plan: %s", ip.Object.Name)
					ip.Object.Spec.Approved = true
					_, err = ip.Update()
					Expect(err).ToNot(HaveOccurred(), "failed approving install plan")
					break
				}
			}

			By("Await operator to be upgraded")
			err = await.OperatorUpgrade(APIClient, ModulesConfig.UpgradeTargetVersion, 5*time.Minute)
			Expect(err).ToNot(HaveOccurred(), "failed awaiting subscription upgrade")

			By("Verify module is still loaded after upgrade")
			err = check.ModuleLoaded(APIClient, kmodName, time.Minute)
			Expect(err).ToNot(HaveOccurred(), "module should remain loaded after operator upgrade")

			By("Check module label is still set on nodes after upgrade")
			_, err = check.NodeLabel(APIClient, moduleName, upgradeTestNamespace,
				GeneralConfig.WorkerLabelMap)
			Expect(err).ToNot(HaveOccurred(), "module labels should remain after upgrade")
		})
	})
})
