/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"reflect"
	"time"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cachev1alpha1 "github.com/MidhunJithu/memcached-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

// MemcachedReconciler reconciles a Memcached object
type MemcachedReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=cache.example.com,resources=memcacheds,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cache.example.com,resources=memcacheds/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cache.example.com,resources=memcacheds/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Memcached object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile
func (r *MemcachedReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	// Fetch the Memcached instance
	memcached := &cachev1alpha1.Memcached{}
	err := r.Get(ctx, req.NamespacedName, memcached)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("memcached instance not found, ignoring since the object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "failed to  get memcached")
		return ctrl.Result{}, err
	}
	log = log.WithValues(
		"Memcached namespace", memcached.Namespace,
		"Memcached Name", memcached.Name,
	)

	// Check if the deployment already exists, if not create a new one
	deploy := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Namespace: memcached.Namespace, Name: memcached.Name}, deploy)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("creating a new deployment for the memcached")
			deploy, err = r.deploymentForMemcached(memcached)
			if err != nil {
				log.Error(err, "failed to generate deployment yaml for memcached")
				return ctrl.Result{}, err
			}
			if err = r.Create(ctx, deploy); err != nil {
				log.Error(err, "failed to create deployment from the memcached")
				return ctrl.Result{}, err
			}
			log.Info("created a new deployment for the memcached")
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(err, "failed to get the deployment details")
		return ctrl.Result{}, err
	}

	// Ensure the deployment size is the same as the spec
	size := memcached.Spec.Size
	if *deploy.Spec.Replicas != size {
		deploy.Spec.Replicas = &size
		if err = r.Update(ctx, deploy); err != nil {
			log.Error(err, "failed to update the deployment")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Minute * 1}, nil
	}

	// Update the Memcached status with the pod names
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(deploy.Namespace),
		client.MatchingLabels(labelsForMemcached(memcached.Name)),
	}
	if err = r.List(ctx, podList, listOpts...); err != nil {
		log.Error(err, "failed to list the memcached pods")
		return ctrl.Result{}, err
	}
	podNames := getPodNames(podList.Items)
	if !reflect.DeepEqual(podNames, memcached.Status.Nodes) {
		memcached.Status.Nodes = podNames
		if err = r.Status().Update(ctx, memcached); err != nil {
			log.Error(err, "failed to update the status")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MemcachedReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cachev1alpha1.Memcached{}).
		Named("memcached").
		Complete(r)
}

func (r *MemcachedReconciler) deploymentForMemcached(memcached *cachev1alpha1.Memcached) (*appsv1.Deployment, error) {
	replicas := memcached.Spec.Size
	mLables := labelsForMemcached(memcached.Name)
	deploy := &appsv1.Deployment{
		ObjectMeta: v1.ObjectMeta{
			Name:      memcached.Name,
			Namespace: memcached.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &v1.LabelSelector{
				MatchLabels: mLables,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: v1.ObjectMeta{
					Labels: mLables,
				},
				Spec: corev1.PodSpec{
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: pointer.Bool(true),
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{
						{
							Name:    "memcached",
							Image:   "memcached:1.4.36-alpine",
							Command: []string{"memcached", "-m=64", "-o", "modern", "-v"},
							Ports: []corev1.ContainerPort{
								{
									Name:          "memcached",
									ContainerPort: 11211,
								},
							},
							SecurityContext: &corev1.SecurityContext{
								RunAsNonRoot:             pointer.Bool(true),
								AllowPrivilegeEscalation: pointer.Bool(false),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{
										"ALL",
									},
								},
								RunAsUser: pointer.Int64(1000),
							},
						},
					},
				},
			},
		},
	}

	return deploy, ctrl.SetControllerReference(memcached, deploy, r.Scheme)
}

// labelsForMemcached returns the labels for selecting the resources
// belonging to the given memcached CR name.
func labelsForMemcached(name string) map[string]string {
	return map[string]string{"app": "memcached", "memcached_cr": name}
}

// getPodNames returns the pod names of the array of pods passed in
func getPodNames(pods []corev1.Pod) []string {
	var podNames []string
	for _, pod := range pods {
		podNames = append(podNames, pod.Name)
	}
	return podNames
}
