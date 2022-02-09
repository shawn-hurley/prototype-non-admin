/*
Copyright 2022.

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

package controllers

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	veleroiov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

// BackupStorageLocationReconciler reconciles a BackupStorageLocation object
type BackupStorageLocationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=velero.io.my.domain,resources=backupstoragelocations,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=velero.io.my.domain,resources=backupstoragelocations/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=velero.io.my.domain,resources=backupstoragelocations/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the BackupStorageLocation object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.10.0/pkg/reconcile
func (r *BackupStorageLocationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	bsl := &veleroiov1.BackupStorageLocation{}

	//Handle Upserts.
	if req.Namespace == "openshift-adp" {
		// This means that we need to upsert the information.

		// Get the status info.
		err := r.Client.Get(ctx, req.NamespacedName, bsl)
		if err != nil {
			log.Error(err, "unable to get Backup Storage Location")
			return ctrl.Result{}, err
		}
		namesapceName, err := getKeyFromAnnotations(bsl.Annotations)
		if err != nil {
			return ctrl.Result{}, nil
		}

		upsertBSL := &veleroiov1.BackupStorageLocation{}
		err = r.Client.Get(ctx, client.ObjectKey{Namespace: namesapceName.Namespace, Name: namesapceName.Name}, upsertBSL)
		if err != nil {
			log.Error(err, "unable to get Backup Storage Location")
			return ctrl.Result{}, err
		}

		upsertBSL.Status = bsl.Status
		err = r.Client.Status().Update(ctx, upsertBSL)
		if err != nil {
			log.Error(err, "unable to update Backup Storage Location status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Handle Deletions (add Finalizers to both)
	// Add secrets that we care about to a list of secrets to add event for.
	// Make things better in terms of

	err := r.Client.Get(ctx, req.NamespacedName, bsl)
	if err != nil {
		log.Error(err, "unable to get Backup Storage Location")
		return ctrl.Result{}, err
	}

	//Get the secret from the credential.

	secret := &corev1.Secret{}

	err = r.Client.Get(ctx, client.ObjectKey{Name: bsl.Spec.Credential.Name, Namespace: bsl.GetNamespace()}, secret)
	if err != nil {
		log.Error(err, "unable to get Secret for credentials Location")
		return ctrl.Result{}, err
	}

	secret2 := secret.DeepCopy()

	// Hash this in the future. and label it to avoid DNS name issues
	objectKeyforBSL := client.ObjectKey{Namespace: bsl.GetNamespace(), Name: bsl.GetName()}
	objectKeyforSecret := client.ObjectKey{Namespace: secret.GetNamespace(), Name: secret.GetName()}

	bsl2 := bsl.DeepCopy()

	// Determine if name is found.
	genName := nameCache.Get(fmt.Sprintf("%v-%v", bsl.Namespace, bsl.Name))
	if genName == "" {
		bsl2.GenerateName = fmt.Sprintf("%v-%v", bsl.GetNamespace(), bsl.GetName())
	}
	bsl2.Name = genName

	// Make this a configuration value in the future
	bsl2.Namespace = "openshift-adp"
	bsl2.ResourceVersion = ""
	secret2.Namespace = "openshift-adp"
	//TODO: handle if this is too long.
	secret2.Name = fmt.Sprintf("%v-%v-%v", bsl.Namespace, bsl.Name, secret.Name)
	secret2.ResourceVersion = ""

	res, err := controllerutil.CreateOrUpdate(ctx, r.Client, bsl2, func() error {
		bsl2.Spec = bsl.Spec
		bsl2.Spec.Credential.Name = secret2.Name
		bsl2.Annotations = addKeyToAnnotations(bsl.Annotations, objectKeyforBSL)
		return nil
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		log.Error(err, "unable to create copied backup storage location")
		return ctrl.Result{}, err
	}
	if res == controllerutil.OperationResultCreated {
		log.Info("created new BSL", "name", bsl2.GetName(), "namespace", bsl2.GetNamespace())
	}
	if res == controllerutil.OperationResultUpdated {
		log.Info("update BSL", "name", bsl2.GetName(), "namespace", bsl2.GetNamespace())
	}
	bsl2.SetGroupVersionKind(bsl.GroupVersionKind())
	nameCache.Add(fmt.Sprintf("%v-%v", bsl.Namespace, bsl.Name), bsl2.Name)

	// Hash this in the future. and label it to avoid DNS name issues

	res, err = controllerutil.CreateOrUpdate(ctx, r.Client, secret2, func() error {
		// Need to figure this out.
		// owner := v1.OwnerReference{
		// 	APIVersion: bsl2.APIVersion,
		// 	Kind:       bsl2.Kind,
		// 	Name:       bsl2.Name,
		// 	UID:        bsl.UID,
		// }
		// secret2.OwnerReferences = append(secret.OwnerReferences, owner)
		secret2.Data = secret.Data
		secret2.Annotations = addKeyToAnnotations(secret.Annotations, objectKeyforSecret)
		return nil
	})
	//Copy BSL
	if err != nil && !errors.IsAlreadyExists(err) {
		log.Error(err, "unable to create copied Secret")
		return ctrl.Result{}, err
	}
	if res == controllerutil.OperationResultCreated {
		log.Info("created new carried secret", "name", secret2.GetName(), "namespace", secret2.GetNamespace())
	}
	if res == controllerutil.OperationResultUpdated {
		log.Info("update carried secret", "name", secret2.GetName(), "namespace", secret2.GetNamespace())
	}
	if res == controllerutil.OperationResultNone {
		log.Info("carried secret correct", "name", secret2.GetName(), "namespace", secret2.GetNamespace())
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BackupStorageLocationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&veleroiov1.BackupStorageLocation{}).
		Complete(r)
}
