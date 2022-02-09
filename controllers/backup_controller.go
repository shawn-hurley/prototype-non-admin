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

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	veleroiov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

// BackupStorageLocationReconciler reconciles a BackupStorageLocation object
type BackupReconciler struct {
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
func (r *BackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	backup := &veleroiov1.Backup{}
	err := r.Client.Get(ctx, req.NamespacedName, backup)
	if err != nil {
		log.Error(err, "unable to get Backup")
		return ctrl.Result{}, err
	}
	//Handle Upserts.
	if req.Namespace == "openshift-adp" {
		// Determine BSL that is associated with this backup.
		// If there is not one continue on.

		// Determine if backup is of a backup that should be upserted.
		objectKeyForUpsert, err := getKeyFromAnnotations(backup.Annotations)
		if err != nil && err == errAnnotationNotFound {
			// Unable to find, lets determine if it is associated with a BSL that should be upserted.
			storageLocation := ""
			for key, value := range backup.Labels {
				if key == "velero.io/storage-location" {
					storageLocation = value
					break
				}
			}
			//This backup MUST be for the openshift-adp namespace, we will do nothing with it.
			if storageLocation == "" {
				return ctrl.Result{}, nil
			}

			oadpBSL := &veleroiov1.BackupStorageLocation{}
			err := r.Client.Get(ctx, client.ObjectKey{Namespace: "openshift-adp", Name: storageLocation}, oadpBSL)
			if err != nil {
				log.Error(err, "unable to get Backup")
				return ctrl.Result{}, err
			}
			log.Info("HEREEERERERE!!!!!! syncing backup", "backup", backup.GetName())
			upsertBSLObjectKey, err := getKeyFromAnnotations(oadpBSL.Annotations)
			if err != nil {
				return ctrl.Result{}, nil
			}
			//copy backup
			upsertBackup := backup.DeepCopy()
			// upsertBackup.GenerateName = backup.Name
			upsertBackup.Name = fmt.Sprintf("%v-%v", upsertBSLObjectKey.Name, backup.Name)
			upsertBackup.ResourceVersion = ""
			upsertBackup.Namespace = upsertBSLObjectKey.Namespace

			res, err := controllerutil.CreateOrUpdate(ctx, r.Client, upsertBackup, func() error {
				upsertBackup.Labels["velero.io/storage-location"] = upsertBSLObjectKey.Name
				return nil
			})
			if err != nil {
				log.Error(err, "unable to create copied backup")
				return ctrl.Result{}, err
			}
			if res == controllerutil.OperationResultCreated {
				log.Info("created new carried backup", "name", upsertBackup.GetName(), "namespace", upsertBackup.GetNamespace())
			}
			if res == controllerutil.OperationResultUpdated {
				log.Info("update carried backup", "name", upsertBackup.GetName(), "namespace", upsertBackup.GetNamespace())
			}
			if res == controllerutil.OperationResultNone {
				log.Info("carried backup correct", "name", upsertBackup.GetName(), "namespace", upsertBackup.GetNamespace())
			}
			return ctrl.Result{}, nil
		}

		// If the backup, should be in a specific place, lets make sure.
		upsertBackup := &veleroiov1.Backup{}
		err = r.Client.Get(ctx, objectKeyForUpsert, upsertBackup)
		// If it is not found, then we should re-create it, this means we need to make it have similar values as it did before.
		if errors.IsNotFound(err) {
			upsertBackup = backup.DeepCopy()
			upsertBackup.ResourceVersion = ""
			upsertBackup.Name = objectKeyForUpsert.Name
			upsertBackup.Namespace = objectKeyForUpsert.Namespace
			// Get BSL
			oadpBSL := &veleroiov1.BackupStorageLocation{}
			err := r.Client.Get(ctx, client.ObjectKey{Namespace: "openshift-adp", Name: upsertBackup.Spec.StorageLocation}, oadpBSL)
			if err != nil {
				log.Error(err, "unable to get Backup")
				return ctrl.Result{}, err
			}
			upsertBSLObjectKey, err := getKeyFromAnnotations(oadpBSL.Annotations)
			if err != nil {
				return ctrl.Result{}, nil
			}
			// Now we know the name of the BSL
			upsertBackup.Spec.StorageLocation = upsertBSLObjectKey.Name
			upsertBackup.Labels["velero.io/storage-location"] = upsertBSLObjectKey.Name
			r.Client.Create(ctx, upsertBackup)
			if err != nil {
				log.Error(err, "unable to get upsert backup", "upsert object", objectKeyForUpsert.String())
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}

		if err != nil {
			log.Error(err, "unable to get upsert backup", "upsert object", objectKeyForUpsert.String())
			return ctrl.Result{}, err
		}

		upsertBackup.Status = backup.Status
		err = r.Client.Update(ctx, upsertBackup)
		if err != nil {
			log.Error(err, "unable to update upsert backup status", "upsert name", upsertBackup.GetName(), "upsert namespace", upsertBackup.GetNamespace())
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	if backup.Status.CompletionTimestamp != nil {
		// backup is complete, means that we don't need to care about this
		return ctrl.Result{}, nil
	}

	backup2 := backup.DeepCopy()
	// Update BSL information.
	backup2.GenerateName = fmt.Sprintf("%v-%v", backup.GetNamespace(), backup.GetName())
	backup2.Namespace = "openshift-adp"
	backup2.ResourceVersion = ""

	objectKeyForBackup := client.ObjectKey{Namespace: backup.GetNamespace(), Name: backup.GetName()}

	res, err := controllerutil.CreateOrUpdate(ctx, r.Client, backup2, func() error {
		backup2.Spec.StorageLocation = nameCache.Get(fmt.Sprintf("%v-%v", backup.GetNamespace(), backup.Spec.StorageLocation))
		backup2.Annotations = addKeyToAnnotations(backup.Annotations, objectKeyForBackup)
		return nil
	})

	if err != nil && !errors.IsAlreadyExists(err) {
		log.Error(err, "unable to create copied Secret")
		return ctrl.Result{}, err
	}
	if res == controllerutil.OperationResultCreated {
		log.Info("created new carried backup", "name", backup2.GetName(), "namespace", backup2.GetNamespace())
	}
	if res == controllerutil.OperationResultUpdated {
		log.Info("update carried backup", "name", backup2.GetName(), "namespace", backup2.GetNamespace())
	}
	if res == controllerutil.OperationResultNone {
		log.Info("carried backup correct", "name", backup2.GetName(), "namespace", backup2.GetNamespace())
	}

	// Handle Deletions (add Finalizers to both)
	// Add secrets that we care about to a list of secrets to add event for.
	// Make things better in terms of

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&veleroiov1.Backup{}).
		Complete(r)
}
