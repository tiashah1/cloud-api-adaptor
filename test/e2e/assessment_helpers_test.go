package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/e2e-framework/klient"
)

func reverseSlice(slice []string) []string {
	length := len(slice)
	for i := 0; i < length/2; i++ {
		slice[i], slice[length-i-1] = slice[length-i-1], slice[i]
	}
	return slice
}

// timeExtractor for comparing and extracting time from a Log String
func timeExtractor(log string) (string, error) {
	matchString := regexp.MustCompile(`\b(\d{2}):(\d{2}):(\d{2})\b`).FindStringSubmatch(log)
	if len(matchString) != 4 {
		return "", errors.New("Invalid Time Data")
	}
	return matchString[0], nil
}

type PodEvents struct {
	EventType        string
	EventDescription string
	EventReason      string
}

func newTestCase(t *testing.T, testName string, assert CloudAssert, assessMessage string) *testCase {
	testCase := &testCase{
		testing:        t,
		testName:       testName,
		assert:         assert,
		assessMessage:  assessMessage,
		podState:       v1.PodRunning,
		imagePullTimer: false,
		isAuth:         false,
	}

	return testCase
}

func podEventExtractor(ctx context.Context, client klient.Client, pod v1.Pod) (*PodEvents, error) {
	clientset, err := kubernetes.NewForConfig(client.RESTConfig())
	if err != nil {
		return nil, err
	}
	watcher, err := clientset.CoreV1().Events(pod.Namespace).Watch(ctx, metav1.ListOptions{FieldSelector: fmt.Sprintf("involvedObject.name=%s", pod.Name)})
	if err != nil {
		return nil, err
	}
	defer watcher.Stop()
	for event := range watcher.ResultChan() {

		if event.Object.(*v1.Event).Type == v1.EventTypeWarning {
			var newPodEvents PodEvents
			newPodEvents.EventType = event.Object.(*v1.Event).Type
			newPodEvents.EventDescription = event.Object.(*v1.Event).Message
			newPodEvents.EventType = event.Object.(*v1.Event).Reason
			return &newPodEvents, nil
		}
	}
	return nil, errors.New("No Events Found in PodVM")
}

func watchImagePullTime(ctx context.Context, client klient.Client, caaPod v1.Pod, Pod v1.Pod) (string, error) {
	pullingtime := ""
	podLogString := ""
	var startTime, endTime time.Time
	clientset, err := kubernetes.NewForConfig(client.RESTConfig())
	if err != nil {
		return "", err
	}

	if Pod.Status.Phase == v1.PodRunning {
		req := clientset.CoreV1().Pods(caaPod.ObjectMeta.Namespace).GetLogs(caaPod.ObjectMeta.Name, &v1.PodLogOptions{})
		podLogs, err := req.Stream(ctx)
		if err != nil {
			return "", err
		}
		defer podLogs.Close()
		buf := new(bytes.Buffer)
		_, err = io.Copy(buf, podLogs)
		if err != nil {
			return "", err
		}
		podLogString = buf.String()

		if podLogString != "" {
			podLogSlice := reverseSlice(strings.Split(podLogString, "\n"))
			for _, i := range podLogSlice {
				if strings.Contains(i, "calling PullImage for \""+Pod.Spec.Containers[0].Image+"\"") {
					timeString, err := timeExtractor(i)
					if err != nil {
						return "", err
					}
					startTime, err = time.Parse("15:04:05", timeString)
					if err != nil {
						return "", err
					}
					break
				}
				if strings.Contains(i, "successfully pulled image \""+Pod.Spec.Containers[0].Image+"\"") {
					timeString, err := timeExtractor(i)
					if err != nil {
						return "", err
					}
					endTime, err = time.Parse("15:04:05", timeString)
					if err != nil {
						return "", err
					}
				}
			}
		} else {
			return "", errors.New("Pod Failed to Log expected Output")
		}
	} else {
		return "", errors.New("Pod Failed to Start")
	}

	pullingtime = endTime.Sub(startTime).String()
	return pullingtime, nil
}

func comparePodLogString(ctx context.Context, client klient.Client, customPod v1.Pod, expectedPodlogString string) (string, error) {
	podLogString := ""
	var podlist v1.PodList
	clientset, err := kubernetes.NewForConfig(client.RESTConfig())
	if err != nil {
		return podLogString, err
	}
	if err := client.Resources(customPod.Namespace).List(ctx, &podlist); err != nil {
		return podLogString, err
	}
	//adding sleep time to intialize container and ready for logging
	time.Sleep(5 * time.Second)
	for _, pod := range podlist.Items {
		if pod.ObjectMeta.Name == customPod.Name {
			func() {
				req := clientset.CoreV1().Pods(customPod.Namespace).GetLogs(pod.ObjectMeta.Name, &v1.PodLogOptions{})
				podLogs, err := req.Stream(ctx)
				if err != nil {
					return
				}
				defer podLogs.Close()
				buf := new(bytes.Buffer)
				_, err = io.Copy(buf, podLogs)
				if err != nil {
					return
				}
				podLogString = strings.TrimSpace(buf.String())
			}()
		}
	}

	if err != nil {
		return podLogString, err
	}

	if !strings.Contains(podLogString, expectedPodlogString) {
		return podLogString, errors.New("Error: Pod Log doesn't contain Expected String")
	}

	return podLogString, nil
}

func getSuccessfulAndErroredPods(ctx context.Context, t *testing.T, client klient.Client, job batchv1.Job) (int, int, string, error) {
	podLogString := ""
	errorPod := 0
	successPod := 0
	var podlist v1.PodList
	clientset, err := kubernetes.NewForConfig(client.RESTConfig())
	if err != nil {
		return 0, 0, "", err
	}
	if err := client.Resources(job.Namespace).List(ctx, &podlist); err != nil {
		return 0, 0, "", err
	}
	for _, pod := range podlist.Items {
		if pod.ObjectMeta.Labels["job-name"] == job.Name && pod.Status.Phase == v1.PodPending {
			if pod.Status.ContainerStatuses[0].State.Waiting.Reason == "ContainerCreating" {
				return 0, 0, "", errors.New("Failed to Create PodVM")
			}
		}
		if pod.ObjectMeta.Labels["job-name"] == job.Name && pod.Status.ContainerStatuses[0].State.Terminated.Reason == "StartError" {
			errorPod++
			t.Log("WARNING:", pod.ObjectMeta.Name, "-", pod.Status.ContainerStatuses[0].State.Terminated.Reason)
		}
		if pod.ObjectMeta.Labels["job-name"] == job.Name && pod.Status.ContainerStatuses[0].State.Terminated.Reason == "Completed" {
			successPod++
			watcher, err := clientset.CoreV1().Events(job.Namespace).Watch(ctx, metav1.ListOptions{})
			if err != nil {
				return 0, 0, "", err
			}
			defer watcher.Stop()
			for event := range watcher.ResultChan() {
				if event.Object.(*v1.Event).Reason == "Started" && pod.Status.ContainerStatuses[0].State.Terminated.Reason == "Completed" {
					func() {
						req := clientset.CoreV1().Pods(job.Namespace).GetLogs(pod.ObjectMeta.Name, &v1.PodLogOptions{})
						podLogs, err := req.Stream(ctx)
						if err != nil {
							return
						}
						defer podLogs.Close()
						buf := new(bytes.Buffer)
						_, err = io.Copy(buf, podLogs)
						if err != nil {
							return
						}
						podLogString = strings.TrimSpace(buf.String())
					}()
					t.Log("SUCCESS:", pod.ObjectMeta.Name, "-", pod.Status.ContainerStatuses[0].State.Terminated.Reason, "- LOG:", podLogString)
					break
				}
			}
		}
	}

	return successPod, errorPod, podLogString, nil
}

func getAuthenticatedImageStatus(ctx context.Context, client klient.Client, expectedStatus string, authpod v1.Pod) error {
	clientset, err := kubernetes.NewForConfig(client.RESTConfig())
	if err != nil {
		return err
	}
	watcher, err := clientset.CoreV1().Events(authpod.ObjectMeta.Namespace).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	defer watcher.Stop()
	for event := range watcher.ResultChan() {
		if event.Object.(*v1.Event).InvolvedObject.Name == authpod.ObjectMeta.Name {
			if event.Object.(*v1.Event).Type == "Normal" && event.Object.(*v1.Event).Reason == "Started" {
				return nil
			}
			if event.Object.(*v1.Event).Type == "Warning" && (strings.Contains(event.Object.(*v1.Event).Message, "failed to authorize") || strings.Contains(event.Object.(*v1.Event).Message, "illegal base64 data at input byte") || strings.Contains(event.Object.(*v1.Event).Message, "401 UNAUTHORIZED")) {
				if expectedStatus == "Completed" {
					return errors.New("Invalid Credentials: " + event.Object.(*v1.Event).Message)
				} else {
					return nil
				}
			}

			if event.Object.(*v1.Event).Type == "Warning" && strings.Contains(event.Object.(*v1.Event).Message, "not found") {
				return errors.New("Invalid Image Name: " + event.Object.(*v1.Event).Message)
			}

			if event.Object.(*v1.Event).Type == "Warning" && strings.Contains(event.Object.(*v1.Event).Message, "failed to pull manifest Not authorized") {
				if expectedStatus == "Completed" {
					return errors.New("Invalid auth-json-secret: " + event.Object.(*v1.Event).Message)
				} else {
					return nil
				}
			}

		}
	}

	return errors.New("PodVM Start Error")
}

// skipTestOnCI skips the test if running on CI
func skipTestOnCI(t *testing.T) {
	ci := os.Getenv("CI")

	if ci == "true" {
		t.Skip("Failing on CI")
	}
}

func testStringEmpty(data string) bool {
	if data == "" {
		return true
	} else {
		return false
	}
}

func testErrorEmpty(err error) bool {
	if err == nil {
		return true
	} else {
		return false
	}
}
