//   Copyright 2016 Wercker Holding BV
//
//   Licensed under the Apache License, Version 2.0 (the "License");
//   you may not use this file except in compliance with the License.
//   You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
//   Unless required by applicable law or agreed to in writing, software
//   distributed under the License is distributed on an "AS IS" BASIS,
//   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//   See the License for the specific language governing permissions and
//   limitations under the License.

package stern

import (
	"context"
	"fmt"
	"regexp"

	"github.com/pkg/errors"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/watch"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

// Target is a target to watch
type Target struct {
	Namespace string
	Pod       string
	Container string
}

// GetID returns the ID of the object
func (t *Target) GetID() string {
	return fmt.Sprintf("%s-%s-%s", t.Namespace, t.Pod, t.Container)
}

// Watch starts listening to Kubernetes events and emits modified
// containers/pods. The first result is targets added, the second is targets
// removed
func Watch(ctx context.Context, i v1.PodInterface, podFilter *regexp.Regexp, containerFilter *regexp.Regexp, containerExcludeFilter *regexp.Regexp, initContainers bool, containerState ContainerState, labelSelector labels.Selector) (chan *Target, chan *Target, error) {
	var podNameList []string
	watcher, err := i.Watch(metav1.ListOptions{Watch: true, LabelSelector: labelSelector.String()})
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to set up watch")
	}

	added := make(chan *Target)
	removed := make(chan *Target)

	go func() {
		for {
			select {
			case e := <-watcher.ResultChan():
				if e.Object == nil {
					// Closed because of error
					return
				}

				pod, ok := e.Object.(*corev1.Pod)
				if !ok {
					continue
				}

				if !podFilter.MatchString(pod.Name) {
					continue
				}

				switch e.Type {
				case watch.Added, watch.Modified:
					var statuses []corev1.ContainerStatus
					statuses = append(statuses, pod.Status.ContainerStatuses...)
					if initContainers {
						statuses = append(statuses, pod.Status.InitContainerStatuses...)
					}

					for _, c := range statuses {
						if !containerFilter.MatchString(c.Name) {
							continue
						}
						if containerExcludeFilter != nil && containerExcludeFilter.MatchString(c.Name) {
							continue
						}

						t := &Target{
							Namespace: pod.Namespace,
							Pod:       pod.Name,
							Container: c.Name,
						}
						if containerState.Match(c.State) {
							podNameList = append(podNameList, pod.Name)
							added <- t
						} else {
							if containerState.has(ALL) {
								if contains(podNameList, pod.Name) == -1 {
									podNameList = append(podNameList, pod.Name)
									added <- t
								}
							} else {
								// Remove the element at index from podNameList.
								index := contains(podNameList, pod.Name)
								if index != -1 {
									copy(podNameList[index:], podNameList[index+1:])
									podNameList[len(podNameList)-1] = ""
									podNameList = podNameList[:len(podNameList)-1]
								}
								removed <- t
							}
						}
					}
				case watch.Deleted:
					var containers []corev1.Container
					containers = append(containers, pod.Spec.Containers...)
					if initContainers {
						containers = append(containers, pod.Spec.InitContainers...)
					}

					for _, c := range containers {
						if !containerFilter.MatchString(c.Name) {
							continue
						}
						if containerExcludeFilter != nil && containerExcludeFilter.MatchString(c.Name) {
							continue
						}

						// Remove the element at index from podNameList.
						index := contains(podNameList, pod.Name)
						if index != -1 {
							copy(podNameList[index:], podNameList[index+1:])
							podNameList[len(podNameList)-1] = ""
							podNameList = podNameList[:len(podNameList)-1]
						}

						removed <- &Target{
							Namespace: pod.Namespace,
							Pod:       pod.Name,
							Container: c.Name,
						}

					}
				}
			case <-ctx.Done():
				watcher.Stop()
				close(added)
				close(removed)
				return
			}
		}
	}()

	return added, removed, nil
}

func contains(podNameSlice []string, podNameItem string) int {
	for index, podName := range podNameSlice {
		if podName == podNameItem {
			return index
		}
	}
	return -1
}
