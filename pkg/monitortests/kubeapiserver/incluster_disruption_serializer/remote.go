package incluster_disruption_serializer

import (
	"context"
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"time"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/origin/pkg/monitortestlibrary/disruptionlibrary"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"

	corev1 "k8s.io/api/core/v1"

	exutil "github.com/openshift/origin/test/extended/util"

	configclient "github.com/openshift/client-go/config/clientset/versioned"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/origin/pkg/monitor/monitorapi"
	"github.com/openshift/origin/pkg/monitortestframework"
	"github.com/openshift/origin/pkg/test/ginkgo/junitapi"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"
)

var (
	//go:embed manifests/namespace.yaml
	namespaceYaml []byte
	//go:embed manifests/crb-hostaccess.yaml
	rbacPrivilegedYaml []byte
	//go:embed manifests/role-monitor.yaml
	rbacMonitorRoleYaml []byte
	//go:embed manifests/crb-monitor.yaml
	rbacListOauthClientCRBYaml []byte
	//go:embed manifests/serviceaccount.yaml
	serviceAccountYaml []byte
	//go:embed manifests/dep-internal-lb.yaml
	internalLBDeploymentYaml []byte
	//go:embed manifests/dep-service-network.yaml
	serviceNetworkDeploymentYaml []byte
	//go:embed manifests/dep-localhost.yaml
	localhostDeploymentYaml []byte
	rbacPrivilegedCRBName   string
	rbacMonitorRoleName     string
	rbacMonitorCRBName      string
)

type InvariantInClusterDisruption struct {
	getImagePullSpec monitortestframework.OpenshiftTestImageGetterFunc

	namespaceName               string
	openshiftTestsImagePullSpec string
	notSupportedReason          string
	allNodes                    int32
	controlPlaneNodes           int32

	adminRESTConfig *rest.Config
	kubeClient      kubernetes.Interface
}

func NewInvariantInClusterDisruption(info monitortestframework.MonitorTestInitializationInfo) monitortestframework.MonitorTest {
	return &InvariantInClusterDisruption{
		getImagePullSpec: info.GetOpenshiftTestsImagePullSpec,
	}
}

func (i *InvariantInClusterDisruption) createDeploymentAndWaitToRollout(ctx context.Context, deploymentObj *appsv1.Deployment) error {

	deploymentObj.Namespace = i.namespaceName
	// we need to use the openshift-tests image of the destination during an upgrade.
	deploymentObj.Spec.Template.Spec.Containers[0].Image = i.openshiftTestsImagePullSpec

	client := i.kubeClient.AppsV1().Deployments(i.namespaceName)
	var err error
	_, err = client.Create(ctx, deploymentObj, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("error creating daemonset: %v", err)
	}

	timeLimitedCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	if _, watchErr := watchtools.UntilWithSync(timeLimitedCtx,
		cache.NewListWatchFromClient(
			i.kubeClient.AppsV1().RESTClient(), "deployments", i.namespaceName, fields.OneTermEqualSelector("metadata.name", deploymentObj.Name)),
		&appsv1.Deployment{},
		nil,
		func(event watch.Event) (bool, error) {
			deployment := event.Object.(*appsv1.Deployment)
			return deployment.Status.ReadyReplicas > 0, nil
		},
	); watchErr != nil {
		return fmt.Errorf("deployment %s didn't roll out: %v", deploymentObj.Name, watchErr)
	}
	return nil
}

func (i *InvariantInClusterDisruption) createInternalLBDeployment(ctx context.Context, apiIntHost string) error {
	deploymentObj := resourceread.ReadDeploymentV1OrDie(internalLBDeploymentYaml)
	deploymentObj.Spec.Template.Spec.Containers[0].Env[0].Value = apiIntHost
	// set amount of deployment replicas to make sure it runs on all nodes
	deploymentObj.Spec.Replicas = &i.allNodes

	return i.createDeploymentAndWaitToRollout(ctx, deploymentObj)
}

func (i *InvariantInClusterDisruption) createServiceNetworkDeployment(ctx context.Context) error {
	deploymentObj := resourceread.ReadDeploymentV1OrDie(serviceNetworkDeploymentYaml)
	// set amount of deployment replicas to make sure it runs on all nodes
	deploymentObj.Spec.Replicas = &i.allNodes

	return i.createDeploymentAndWaitToRollout(ctx, deploymentObj)
}

func (i *InvariantInClusterDisruption) createLocalhostDeployment(ctx context.Context) error {
	deploymentObj := resourceread.ReadDeploymentV1OrDie(localhostDeploymentYaml)
	// set amount of deployment replicas to make sure it runs on control plane nodes
	deploymentObj.Spec.Replicas = &i.controlPlaneNodes

	return i.createDeploymentAndWaitToRollout(ctx, deploymentObj)
}

func (i *InvariantInClusterDisruption) createRBACPrivileged(ctx context.Context) error {
	rbacPrivilegedObj := resourceread.ReadClusterRoleBindingV1OrDie(rbacPrivilegedYaml)
	rbacPrivilegedObj.Subjects[0].Namespace = i.namespaceName

	client := i.kubeClient.RbacV1().ClusterRoleBindings()
	_, err := client.Create(ctx, rbacPrivilegedObj, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("error creating privileged SCC CRB: %v", err)
	}
	rbacPrivilegedCRBName = rbacPrivilegedObj.Name
	return nil
}

func (i *InvariantInClusterDisruption) createMonitorRole(ctx context.Context) error {
	rbacMonitorRoleObj := resourceread.ReadClusterRoleV1OrDie(rbacMonitorRoleYaml)
	rbacMonitorRoleName = rbacMonitorRoleObj.Name

	client := i.kubeClient.RbacV1().ClusterRoles()
	_, err := client.Create(ctx, rbacMonitorRoleObj, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("error creating oauthclients list role: %v", err)
	}
	rbacMonitorRoleName = rbacMonitorRoleObj.Name
	return nil
}

func (i *InvariantInClusterDisruption) createMonitorCRB(ctx context.Context) error {
	rbacMonitorCRBObj := resourceread.ReadClusterRoleBindingV1OrDie(rbacListOauthClientCRBYaml)
	rbacMonitorCRBObj.Subjects[0].Namespace = i.namespaceName
	rbacMonitorCRBName = rbacMonitorCRBObj.Name

	client := i.kubeClient.RbacV1().ClusterRoleBindings()
	_, err := client.Create(ctx, rbacMonitorCRBObj, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("error creating oauthclients list CRB: %v", err)
	}
	rbacMonitorCRBName = rbacMonitorCRBObj.Name
	return nil
}

func (i *InvariantInClusterDisruption) createServiceAccount(ctx context.Context) error {
	serviceAccountObj := resourceread.ReadServiceAccountV1OrDie(serviceAccountYaml)
	serviceAccountObj.Namespace = i.namespaceName
	client := i.kubeClient.CoreV1().ServiceAccounts(i.namespaceName)
	_, err := client.Create(ctx, serviceAccountObj, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("error creating service account: %v", err)
	}
	return nil
}

func (i *InvariantInClusterDisruption) createNamespace(ctx context.Context) error {
	namespaceObj := resourceread.ReadNamespaceV1OrDie(namespaceYaml)

	client := i.kubeClient.CoreV1().Namespaces()
	actualNamespace, err := client.Create(ctx, namespaceObj, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("error creating namespace: %v", err)
	}
	i.namespaceName = actualNamespace.Name
	return nil
}

func (i *InvariantInClusterDisruption) StartCollection(ctx context.Context, adminRESTConfig *rest.Config, _ monitorapi.RecorderWriter) error {
	var err error
	i.openshiftTestsImagePullSpec, i.notSupportedReason, err = i.getImagePullSpec(ctx, adminRESTConfig)
	if err != nil {
		return err
	}
	if len(i.notSupportedReason) > 0 {
		return nil
	}

	i.adminRESTConfig = adminRESTConfig
	i.kubeClient, err = kubernetes.NewForConfig(i.adminRESTConfig)
	if err != nil {
		return err
	}

	if ok, _ := exutil.IsMicroShiftCluster(i.kubeClient); ok {
		i.notSupportedReason = "microshift clusters don't have load balancers"
		return nil
	}

	fmt.Fprintf(os.Stderr, "Starting in-cluster monitoring deployments\n")
	configClient, err := configclient.NewForConfig(i.adminRESTConfig)
	if err != nil {
		return err
	}
	infra, err := configClient.ConfigV1().Infrastructures().Get(context.Background(), "cluster", metav1.GetOptions{})
	if err != nil {
		return err
	}

	internalAPI, err := url.Parse(infra.Status.APIServerInternalURL)
	if err != nil {
		return err
	}
	apiIntHost := internalAPI.Hostname()

	allNodes, err := i.kubeClient.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return err
	}
	i.allNodes = int32(len(allNodes.Items))

	controlPlaneNodes, err := i.kubeClient.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{
		LabelSelector: labels.Set{"node-role.kubernetes.io/master": ""}.AsSelector().String(),
	})
	if err != nil {
		return err
	}
	i.controlPlaneNodes = int32(len(controlPlaneNodes.Items))

	err = i.createNamespace(ctx)
	if err != nil {
		return err
	}

	err = i.createServiceAccount(ctx)
	if err != nil {
		return err
	}
	err = i.createRBACPrivileged(ctx)
	if err != nil {
		return err
	}
	err = i.createMonitorRole(ctx)
	if err != nil {
		return err
	}
	err = i.createMonitorCRB(ctx)
	if err != nil {
		return err
	}
	err = i.createServiceNetworkDeployment(ctx)
	if err != nil {
		return err
	}
	err = i.createLocalhostDeployment(ctx)
	if err != nil {
		return err
	}
	err = i.createInternalLBDeployment(ctx, apiIntHost)
	if err != nil {
		return err
	}
	return nil
}

func (i *InvariantInClusterDisruption) CollectData(ctx context.Context, storageDir string, beginning time.Time, end time.Time) (monitorapi.Intervals, []*junitapi.JUnitTestCase, error) {
	if len(i.notSupportedReason) > 0 {
		return nil, nil, nil
	}

	// create the stop collecting configmap and wait for 30s to thing to have stopped.  the 30s is just a guess
	if _, err := i.kubeClient.CoreV1().ConfigMaps(i.namespaceName).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "stop-collecting"},
	}, metav1.CreateOptions{}); err != nil {
		return nil, nil, err
	}

	// TODO create back-pressure on the configmap
	select {
	case <-time.After(30 * time.Second):
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}

	fmt.Fprintf(os.Stderr, "Collecting data from in-cluster monitoring daemonsets\n")

	pollerLabel, err := labels.NewRequirement("network.openshift.io/disruption-actor", selection.Equals, []string{"poller"})
	if err != nil {
		return nil, nil, err
	}

	intervals, junits, errs := disruptionlibrary.CollectIntervalsForPods(ctx, i.kubeClient, i.namespaceName, labels.NewSelector().Add(*pollerLabel))
	return intervals, junits, utilerrors.NewAggregate(errs)
}

func (i *InvariantInClusterDisruption) ConstructComputedIntervals(ctx context.Context, startingIntervals monitorapi.Intervals, _ monitorapi.ResourcesMap, beginning time.Time, end time.Time) (constructedIntervals monitorapi.Intervals, err error) {
	return nil, nil
}

func (i *InvariantInClusterDisruption) EvaluateTestsFromConstructedIntervals(ctx context.Context, finalIntervals monitorapi.Intervals) ([]*junitapi.JUnitTestCase, error) {
	return nil, nil
}

func (i *InvariantInClusterDisruption) WriteContentToStorage(ctx context.Context, storageDir, timeSuffix string, finalIntervals monitorapi.Intervals, finalResourceState monitorapi.ResourcesMap) error {
	return nil
}

func (i *InvariantInClusterDisruption) Cleanup(ctx context.Context) error {
	if len(i.notSupportedReason) > 0 {
		return nil
	}

	fmt.Fprintf(os.Stderr, "Removing in-cluster monitoring namespace\n")
	kubeClient, err := kubernetes.NewForConfig(i.adminRESTConfig)
	if err != nil {
		return err
	}
	nsClient := kubeClient.CoreV1().Namespaces()
	err = nsClient.Delete(ctx, i.namespaceName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("error removing namespace %s: %v", i.namespaceName, err)
	}

	fmt.Fprintf(os.Stderr, "Removing in-cluster monitoring cluster roles and bindings\n")
	crbClient := kubeClient.RbacV1().ClusterRoleBindings()
	err = crbClient.Delete(ctx, rbacPrivilegedCRBName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("error removing cluster reader CRB: %v", err)
	}

	err = crbClient.Delete(ctx, rbacMonitorCRBName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("error removing monitor CRB: %v", err)
	}

	rolesClient := kubeClient.RbacV1().ClusterRoles()
	err = rolesClient.Delete(ctx, rbacMonitorRoleName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("error removing monitor role: %v", err)
	}
	return nil
}
