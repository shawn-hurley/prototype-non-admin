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

package v1

import (
	"context"
	"fmt"
	"net/http"

	authorization "k8s.io/api/authorization/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/go-logr/logr"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"github.com/vmware-tanzu/velero/pkg/util/boolptr"
)

func SetupWebhookWithManager(mgr manager.Manager) error {

	hookServer := &webhook.Server{}
	if err := mgr.Add(hookServer); err != nil {
		panic(err)
	}

	validatingBackupStorageHook := &webhook.Admission{
		Handler: &bslAdmitter{
			client: mgr.GetClient(),
			log:    mgr.GetLogger(),
		},
	}

	validatingBackupHook := &webhook.Admission{
		Handler: &backupAdmitter{
			client: mgr.GetClient(),
			log:    mgr.GetLogger(),
		},
	}

	validatingRestoreHook := &webhook.Admission{
		Handler: &restoreAdmitter{
			client: mgr.GetClient(),
			log:    mgr.GetLogger(),
		},
	}

	// Register the webhooks in the server.
	hookServer.Register("/validating-backup-storage-location", validatingBackupStorageHook)
	hookServer.Register("/validating-backup", validatingBackupHook)
	hookServer.Register("/validating-restore", validatingRestoreHook)

	return nil
}

type bslAdmitter struct {
	decoder *admission.Decoder
	client  client.Client
	log     logr.Logger
}

func (b *bslAdmitter) Handle(ctx context.Context, req webhook.AdmissionRequest) webhook.AdmissionResponse {
	//Decod Object to BSL

	bsl := &velerov1.BackupStorageLocation{}

	err := b.decoder.Decode(req, bsl)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	if req.Namespace == "openshift-adp" {
		// Don't always allow, determine if it
		b.log.Info("Always allow in openshift-adp")
		return admission.Allowed("")
	}

	if bsl.Spec.Credential == nil {
		return admission.Denied("Must provide Credentials")
	}
	if bsl.Spec.Default {
		return admission.Denied("Can not create a default backup storeage location")
	}

	// validate that the user has access to the secret defined.
	sar := authorization.SubjectAccessReview{
		Spec: authorization.SubjectAccessReviewSpec{
			User:   req.UserInfo.Username,
			Groups: req.UserInfo.Groups,
			//TODO: MUST HANDLE THIS.
			//Extra:  req.UserInfo.Extra,
			UID: req.UserInfo.UID,
			ResourceAttributes: &authorization.ResourceAttributes{
				Namespace: bsl.GetNamespace(),
				Verb:      "get",
				Group:     "corev1",
				Version:   "v1",
				Resource:  "secret",
				Name:      bsl.Spec.Credential.Name,
			},
		},
	}

	err = b.client.Create(ctx, &sar)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	if sar.Status.Allowed {
		return webhook.Allowed("")
	}
	return webhook.Denied("invalid access to credential secret")
}

// InjectDecoder injects the decoder into a validatingHandler.
func (b *bslAdmitter) InjectDecoder(d *admission.Decoder) error {
	b.decoder = d
	return nil
}

//Admit Backup
type backupAdmitter struct {
	decoder *admission.Decoder
	client  client.Client
	log     logr.Logger
}

func (b *backupAdmitter) Handle(ctx context.Context, req webhook.AdmissionRequest) webhook.AdmissionResponse {

	//Decod Object to BSL

	backup := &velerov1.Backup{}

	err := b.decoder.Decode(req, backup)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	//TODO: validation like this should be shared with the controlller.
	if backup.Spec.StorageLocation == "" {
		return admission.Denied("Must provide BSL")
	}

	if boolptr.IsSetToTrue(backup.Spec.IncludeClusterResources) {
		return admission.Denied("Can not use Cluster Scoped Resources")
	}
	if len(backup.Spec.IncludedNamespaces) != 1 {
		//Right now reject everything that is not this not equal to this exact namesapce.
		return admission.Denied("Not a backup for the given namespace only")
	}

	if backup.GetNamespace() != "openshift-adp" && backup.Spec.IncludedNamespaces[0] != backup.GetNamespace() {
		return admission.Denied("Not a backup for the given namespace only")
	}

	// validate that the user has access to the storage location defined.
	sar := authorization.SubjectAccessReview{
		Spec: authorization.SubjectAccessReviewSpec{
			User:   req.UserInfo.Username,
			Groups: req.UserInfo.Groups,
			//TODO: MUST HANDLE THIS.
			//Extra:  req.UserInfo.Extra,
			UID: req.UserInfo.UID,
			ResourceAttributes: &authorization.ResourceAttributes{
				Namespace: backup.GetNamespace(),
				Verb:      "get",
				Group:     "velero.io",
				Version:   "v1",
				Resource:  "backupstoragelocations",
				Name:      backup.Spec.StorageLocation,
			},
		},
	}

	err = b.client.Create(ctx, &sar)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	if sar.Status.Allowed {
		return webhook.Allowed("")
	}
	return webhook.Denied("invalid access to BSL")
}

// InjectDecoder injects the decoder into a validatingHandler.
func (b *backupAdmitter) InjectDecoder(d *admission.Decoder) error {
	b.decoder = d
	return nil
}

//Admit Restore
type restoreAdmitter struct {
	decoder *admission.Decoder
	client  client.Client
	log     logr.Logger
}

func (b *restoreAdmitter) Handle(ctx context.Context, req webhook.AdmissionRequest) webhook.AdmissionResponse {

	//Decod Object to BSL

	restore := &velerov1.Restore{}

	err := b.decoder.Decode(req, restore)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	for _, value := range restore.Spec.NamespaceMapping {
		if value != req.Namespace {
			return admission.Denied(fmt.Sprintf("can not map to a namespace that is not: %v", req.Namespace))
		}
	}

	if restore.Spec.BackupName != "" {
		return admission.Denied("Must provide backup")
	}

	// validate that the user has access to the secret defined.
	sar := authorization.SubjectAccessReview{
		Spec: authorization.SubjectAccessReviewSpec{
			User:   req.UserInfo.Username,
			Groups: req.UserInfo.Groups,
			//TODO: MUST HANDLE THIS.
			//Extra:  req.UserInfo.Extra,
			UID: req.UserInfo.UID,
			ResourceAttributes: &authorization.ResourceAttributes{
				Namespace: restore.GetNamespace(),
				Verb:      "get",
				Group:     "velero.io",
				Version:   "v1",
				Resource:  "backup",
				Name:      restore.Spec.BackupName,
			},
		},
	}

	err = b.client.Create(ctx, &sar)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	if sar.Status.Allowed {
		return webhook.Allowed("")
	}
	return webhook.Denied("invalid access to backup referenced")
}

// InjectDecoder injects the decoder into a validatingHandler.
func (b *restoreAdmitter) InjectDecoder(d *admission.Decoder) error {
	b.decoder = d
	return nil
}
