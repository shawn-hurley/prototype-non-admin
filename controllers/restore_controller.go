package controllers

import (
	"context"
	"fmt"

	veleroiov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type RestoreReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the BackupStorageLocation object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.10.0/pkg/reconcile
func (r *RestoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	restore := &veleroiov1.Restore{}
	err := r.Client.Get(ctx, req.NamespacedName, restore)
	if err != nil {
		log.Error(err, "unable to get restore")
	}

	// If it is openshift-adp, then we should consider that we are upserting or ignoring
	if req.Namespace == "openshift-adp" {
		restoreKey, err := getKeyFromAnnotations(restore.Annotations)
		if err != nil {
			log.Info("Skipping because no upsert needed")
			return ctrl.Result{}, nil
		}
		upsertRestore := &veleroiov1.Restore{}
		err = r.Client.Get(ctx, restoreKey, upsertRestore)
		if err != nil {
			return ctrl.Result{}, err
		}

		upsertRestore.Status = restore.Status
		err = r.Client.Update(ctx, upsertRestore)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Find the backup
	backupOld := &veleroiov1.Backup{}
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: restore.Spec.BackupName}, backupOld)
	if err != nil {
		return ctrl.Result{}, err
	}
	backupList := &veleroiov1.BackupList{}
	err = r.Client.List(ctx, backupList, &client.ListOptions{Namespace: "openshift-adp"})
	if err != nil {
		return ctrl.Result{}, err
	}

	var backupName string
	backupKey := client.ObjectKeyFromObject(backupOld)
	for _, backup := range backupList.Items {
		key, err := getKeyFromAnnotations(backup.Annotations)
		if err != nil {
			continue
		}
		if key == backupKey {
			backupName = backup.Name
			break
		}
	}

	restore2 := restore.DeepCopy()
	restoreObjectKey := client.ObjectKeyFromObject(restore)

	// Set new names
	restore2.GenerateName = fmt.Sprintf("%v-%v", req.Namespace, req.Name)
	restore2.Name = ""
	restore2.Namespace = "openshift-adp"

	res, err := controllerutil.CreateOrUpdate(ctx, r.Client, restore2, func() error {
		restore2.Annotations = addKeyToAnnotations(restore2.Annotations, restoreObjectKey)
		restore2.Spec.BackupName = backupName
		return nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	if res == controllerutil.OperationResultCreated {
		log.Info("created new restore", "name", restore2.GetName(), "namespace", restore2.GetNamespace())
	}
	if res == controllerutil.OperationResultUpdated {
		log.Info("update restore", "name", restore2.GetName(), "namespace", restore2.GetNamespace())
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RestoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&veleroiov1.Restore{}).
		Complete(r)
}
