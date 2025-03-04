package e2e

import (
	"testing"

	batchv1 "k8s.io/api/batch/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type podOption func(*corev1.Pod)

func withRestartPolicy(restartPolicy corev1.RestartPolicy) podOption {
	return func(p *corev1.Pod) {
		p.Spec.RestartPolicy = restartPolicy
	}
}

// Optional method to add ContainerPort and ReadinessProbe to listen Port 80
func withContainerPort(port int32) podOption {
	return func(p *corev1.Pod) {
		p.Spec.Containers[0].Ports = []corev1.ContainerPort{{ContainerPort: port}}
		p.Spec.Containers[0].ReadinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/",
					Port: intstr.FromInt(int(port)),
				},
			},
			InitialDelaySeconds: 10,
			PeriodSeconds:       5,
		}
	}
}

func withCommand(command []string) podOption {
	return func(p *corev1.Pod) {
		p.Spec.Containers[0].Command = command
	}
}

func withEnvironmentalVariables(envVar []corev1.EnvVar) podOption {
	return func(p *corev1.Pod) {
		p.Spec.Containers[0].Env = envVar
	}
}

func withImagePullSecrets(secretName string) podOption {
	return func(p *corev1.Pod) {
		p.Spec.ImagePullSecrets = []corev1.LocalObjectReference{
			{
				Name: secretName,
			},
		}
	}
}

func withConfigMapBinding(mountPath string, configMapName string) podOption {
	return func(p *corev1.Pod) {
		p.Spec.Containers[0].VolumeMounts = append(p.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{Name: "config-volume", MountPath: mountPath})
		p.Spec.Volumes = append(p.Spec.Volumes, corev1.Volume{Name: "config-volume", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: configMapName}}}})
	}
}

func withSecretBinding(mountPath string, secretName string) podOption {
	return func(p *corev1.Pod) {
		p.Spec.Containers[0].VolumeMounts = append(p.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{Name: "secret-volume", MountPath: mountPath})
		p.Spec.Volumes = append(p.Spec.Volumes, corev1.Volume{Name: "secret-volume", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: secretName}}})
	}
}

func withPVCBinding(mountPath string, pvcName string) podOption {
	propagationHostToContainer := corev1.MountPropagationHostToContainer
	return func(p *corev1.Pod) {
		p.Spec.Containers[2].VolumeMounts = append(p.Spec.Containers[2].VolumeMounts, corev1.VolumeMount{Name: "pvc-volume", MountPath: mountPath, MountPropagation: &propagationHostToContainer})
		p.Spec.Volumes = append(p.Spec.Volumes, corev1.Volume{Name: "pvc-volume", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvcName}}})
	}
}

func withAnnotations(data map[string]string) podOption {
	return func(p *corev1.Pod) {
		p.ObjectMeta.Annotations = data
	}
}

func newPod(namespace string, podName string, containerName string, imageName string, options ...podOption) *corev1.Pod {
	runtimeClassName := "kata-remote"
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: namespace},
		Spec: corev1.PodSpec{
			Containers:       []corev1.Container{{Name: containerName, Image: imageName, ImagePullPolicy: corev1.PullAlways}},
			RuntimeClassName: &runtimeClassName,
		},
	}

	for _, option := range options {
		option(pod)
	}

	return pod
}

func newNginxPod(namespace string) *corev1.Pod {
	return newPod(namespace, "nginx", "nginx", "nginx", withRestartPolicy(corev1.RestartPolicyNever))
}

func newNginxPodWithName(namespace string, podName string) *corev1.Pod {
	return newPod(namespace, podName, "nginx", "nginx", withRestartPolicy(corev1.RestartPolicyNever))
}

func newBusyboxPod(namespace string) *corev1.Pod {
	return newPod(namespace, "busybox-pod", "busybox", "quay.io/prometheus/busybox:latest", withCommand([]string{"/bin/sh", "-c", "sleep 3600"}))
}

// newConfigMap returns a new config map object.
func newConfigMap(namespace string, name string, configMapData map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data:       configMapData,
	}
}

// newSecret returns a new secret object.
func newSecret(namespace string, name string, data map[string][]byte, secretType corev1.SecretType) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data:       data,
		Type:       secretType,
	}
}

// newJob returns a new job
func newJob(namespace string, name string) *batchv1.Job {
	runtimeClassName := "kata-remote"
	BackoffLimit := int32(8)
	TerminateGracePeriod := int64(0)
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
				Spec: corev1.PodSpec{
					TerminationGracePeriodSeconds: &TerminateGracePeriod,
					Containers: []corev1.Container{{
						Name:    name,
						Image:   "quay.io/prometheus/busybox:latest",
						Command: []string{"/bin/sh", "-c", "echo 'scale=5; 4*a(1)' | bc -l"},
					}},
					RestartPolicy:    corev1.RestartPolicyNever,
					RuntimeClassName: &runtimeClassName,
				},
			},
			BackoffLimit: &BackoffLimit,
		},
	}
}

// newPVC returns a new pvc object.
func newPVC(namespace, name, storageClassName, diskSize string, accessModel corev1.PersistentVolumeAccessMode) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: &storageClassName,
			AccessModes: []corev1.PersistentVolumeAccessMode{
				accessModel,
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(diskSize),
				},
			},
		},
	}
}

// CloudAssert defines assertions to perform on the cloud provider.
type CloudAssert interface {
	HasPodVM(t *testing.T, id string)                             // Assert there is a PodVM with `id`.
	getInstanceType(t *testing.T, podName string) (string, error) // Get Instance Type of PodVM
}

// RollingUpdateAssert defines assertions for rolling update test
type RollingUpdateAssert interface {
	CachePodVmIDs(t *testing.T, deploymentName string) // Cache Pod VM IDs before rolling update
	VerifyOldVmDeleted(t *testing.T)                   // Verify old Pod VMs have been deleted
}
