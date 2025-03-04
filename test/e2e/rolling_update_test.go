// (C) Copyright Confidential Containers Contributors
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	envconf "sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

const WAIT_DEPLOYMENT_AVAILABLE_TIMEOUT = time.Second * 180
const OLD_VM_DELETION_TIMEOUT = time.Second * 60

func doTestCaaDaemonsetRollingUpdate(t *testing.T, assert RollingUpdateAssert) {
	runtimeClassName := "kata-remote"
	namespace := envconf.RandomName("default", 7)
	deploymentName := "nginx-deployment"
	containerName := "nginx"
	imageName := "nginx"
	serviceName := "nginx-service"
	portName := "port80"
	rc := int32(2)
	labelsMap := map[string]string{
		"app": "nginx",
	}
	verifyPodName := "verify-pod"
	verifyContainerName := "verify-container"
	verifyImageName := "radial/busyboxplus:curl"

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: namespace,
			Labels:    labelsMap,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &rc,
			Selector: &metav1.LabelSelector{
				MatchLabels: labelsMap,
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labelsMap,
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:            containerName,
							Image:           imageName,
							ImagePullPolicy: v1.PullAlways,
						},
					},
					RuntimeClassName: &runtimeClassName,
					Affinity: &v1.Affinity{
						PodAntiAffinity: &v1.PodAntiAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: []v1.PodAffinityTerm{
								{
									LabelSelector: &metav1.LabelSelector{
										MatchExpressions: []metav1.LabelSelectorRequirement{
											{
												Key:      "app",
												Operator: metav1.LabelSelectorOpIn,
												Values:   []string{"nginx"},
											},
										},
									},
									TopologyKey: "kubernetes.io/hostname",
								},
							},
						},
					},
				},
			},
		},
	}

	svc := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: namespace,
		},
		Spec: v1.ServiceSpec{
			Type: v1.ServiceTypeNodePort,
			Ports: []v1.ServicePort{
				{
					Name:       portName,
					Port:       int32(80),
					TargetPort: intstr.FromInt(80),
					Protocol:   v1.ProtocolTCP,
				},
			},
			Selector: labelsMap,
		},
	}

	verifyPod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      verifyPodName,
			Namespace: namespace,
		},
		Spec: v1.PodSpec{
			RestartPolicy: v1.RestartPolicyNever,
			Containers: []v1.Container{
				{
					Name:  verifyContainerName,
					Image: verifyImageName,
					Command: []string{
						"/bin/sh",
						"-c",
						// Not complete command; will append later
					},
				},
			},
		},
	}

	upgradeFeature := features.New("CAA DaemonSet upgrade test").
		WithSetup("Create nginx deployment and service", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatal(err)
			}

			log.Info("Creating nginx deployment...")
			if err = client.Resources().Create(ctx, deployment); err != nil {
				t.Fatal(err)
			}
			waitForDeploymentAvailable(t, client, deployment, rc)
			log.Info("nginx deployment is available now")

			// Cache Pod VM instance IDs before upgrade
			assert.CachePodVmIDs(t, deploymentName)

			log.Info("Creating nginx Service")
			if err = client.Resources().Create(ctx, svc); err != nil {
				t.Fatal(err)
			}
			clusterIP := waitForClusterIP(t, client, svc)
			log.Printf("nginx service is available on cluster IP: %s", clusterIP)

			// Update verify command
			verifyPod.Spec.Containers[0].Command = append(
				verifyPod.Spec.Containers[0].Command,
				fmt.Sprintf(`
						while true; do
						if ! curl -m 5 -IsSf %s:80 > /dev/null; then
							echo "disconnected: $(date)"
							exit 1
						else
							echo "connected: $(date)"
							sleep 1
						fi
						done
				`, clusterIP))
			if err = client.Resources().Create(ctx, verifyPod); err != nil {
				t.Fatal(err)
			}
			if err = wait.For(conditions.New(client.Resources()).PodRunning(verifyPod), wait.WithTimeout(WAIT_POD_RUNNING_TIMEOUT)); err != nil {
				t.Fatal(err)
			}

			return ctx
		}).
		Assess("Access for upgrade test", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatal(err)
			}

			caaDaemonSetName := "cloud-api-adaptor-daemonset"
			caaNamespace := "confidential-containers-system"

			ds := &appsv1.DaemonSet{}
			if err = client.Resources().Get(ctx, caaDaemonSetName, caaNamespace, ds); err != nil {
				t.Fatal(err)
			}
			log.Info("Force to update CAA pods by increasing StartupProbe.FailureThreshold")
			ds.Spec.Template.Spec.Containers[0].StartupProbe.FailureThreshold += 1
			if err = client.Resources().Update(ctx, ds); err != nil {
				t.Fatal(err)
			}

			// Wait for nginx deployment available again
			waitForDeploymentAvailable(t, client, deployment, rc)

			if err = client.Resources().Get(ctx, verifyPodName, namespace, verifyPod); err != nil {
				t.Fatal(err)
			}
			log.Printf("verify pod status: %s", verifyPod.Status.Phase)
			if verifyPod.Status.Phase != v1.PodRunning {
				clientset, err := kubernetes.NewForConfig(client.RESTConfig())
				if err != nil {
					log.Printf("Failed to new client set: %v", err)
				} else {
					req := clientset.CoreV1().Pods(namespace).GetLogs(verifyPodName, &v1.PodLogOptions{})
					podLogs, err := req.Stream(ctx)
					if err != nil {
						log.Printf("Failed to get pod logs: %v", err)
					} else {
						defer podLogs.Close()
						buf := new(bytes.Buffer)
						_, err = io.Copy(buf, podLogs)
						if err != nil {
							log.Printf("Failed to copy pod logs: %v", err)
						} else {
							podLogString := strings.TrimSpace(buf.String())
							log.Printf("verify pod logs: \n%s", podLogString)
						}
					}
				}
				t.Fatal(fmt.Errorf("verify pod is not running"))
			}

			time.Sleep(OLD_VM_DELETION_TIMEOUT)
			log.Info("Verify old VM instances have been deleted:")
			assert.VerifyOldVmDeleted(t)

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatal(err)
			}

			log.Info("Deleting verify pod...")
			if err = client.Resources().Delete(ctx, verifyPod); err != nil {
				t.Fatal(err)
			}

			log.Info("Deleting nginx service...")
			if err = client.Resources().Delete(ctx, svc); err != nil {
				t.Fatal(err)
			}

			log.Info("Deleting nginx deployment...")
			if err = client.Resources().Delete(ctx, deployment); err != nil {
				t.Fatal(err)
			}

			return ctx
		}).Feature()

	testEnv.Test(t, upgradeFeature)
}

func waitForDeploymentAvailable(t *testing.T, client klient.Client, deployment *appsv1.Deployment, rc int32) {
	if err := wait.For(conditions.New(client.Resources()).ResourceMatch(deployment, func(object k8s.Object) bool {
		deployObj, ok := object.(*appsv1.Deployment)
		if !ok {
			log.Printf("Not a Deployment object: %v", object)
			return false
		}

		log.Printf("Current deployment available replicas: %d", deployObj.Status.AvailableReplicas)
		return deployObj.Status.AvailableReplicas == rc
	}), wait.WithTimeout(WAIT_DEPLOYMENT_AVAILABLE_TIMEOUT)); err != nil {
		t.Fatal(err)
	}
}

func waitForClusterIP(t *testing.T, client klient.Client, svc *v1.Service) string {
	var clusterIP string
	if err := wait.For(conditions.New(client.Resources()).ResourceMatch(svc, func(object k8s.Object) bool {
		svcObj, ok := object.(*v1.Service)
		if !ok {
			log.Printf("Not a Service object: %v", object)
			return false
		}
		clusterIP = svcObj.Spec.ClusterIP
		if clusterIP != "" {
			return true
		} else {
			log.Printf("Current service: %v", svcObj)
			return false
		}
	}), wait.WithTimeout(WAIT_DEPLOYMENT_AVAILABLE_TIMEOUT)); err != nil {
		t.Fatal(err)
	}

	return clusterIP
}
