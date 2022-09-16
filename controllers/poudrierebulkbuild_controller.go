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
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	freebsdv1 "github.com/xaque208/freebsd_controller/api/v1"
)

// PoudriereBulkBuildReconciler reconciles a PoudriereBulkBuild object
type PoudriereBulkBuildReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=freebsd.znet,resources=poudrierebulkbuilds,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=freebsd.znet,resources=poudrierebulkbuilds/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=freebsd.znet,resources=poudrierebulkbuilds/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the PoudriereBulkBuild object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.2/pkg/reconcile
func (r *PoudriereBulkBuildReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var poudriereBulkBuild freebsdv1.PoudriereBulkBuild
	if err := r.Get(ctx, req.NamespacedName, &poudriereBulkBuild); err != nil {
		log.Error(err, "unable to fetch PoudriereBulkBuild")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	jailName := types.NamespacedName{
		Namespace: req.Namespace,
		Name:      poudriereBulkBuild.Spec.Jail,
	}

	var poudriereJail freebsdv1.PoudriereJail
	if err := r.Get(ctx, jailName, &poudriereJail); err != nil {
		log.Error(err, fmt.Sprintf("unable to fetch PoudriereJail %q", jailName.Name))
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	treeName := types.NamespacedName{
		Namespace: req.Namespace,
		Name:      poudriereBulkBuild.Spec.Tree,
	}

	var poudrierePortsTree freebsdv1.PoudrierePortsTree
	if err := r.Get(ctx, treeName, &poudrierePortsTree); err != nil {
		log.Error(err, fmt.Sprintf("unable to fetch PoudrierePortsTree %q", treeName.Name))
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !poudrierePortsTree.Status.Ready || !poudriereJail.Status.Ready {
		return ctrl.Result{}, fmt.Errorf("portsTree or jail are not ready")
	}

	myFinalizerName := "poudrierebulkbuild.freebsd.znet/finalizer"

	if poudriereBulkBuild.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !controllerutil.ContainsFinalizer(&poudriereBulkBuild, myFinalizerName) {
			controllerutil.AddFinalizer(&poudriereBulkBuild, myFinalizerName)
			if err := r.Update(ctx, &poudriereBulkBuild); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(&poudriereBulkBuild, myFinalizerName) {
			// our finalizer is present, so lets handle any external dependency
			if err := r.deleteExternalResources(&poudriereBulkBuild); err != nil {
				// if fail to delete the external dependency here, return with error
				// so that it can be retried
				return ctrl.Result{}, err
			}

			// remove our finalizer from the list and update it.
			controllerutil.RemoveFinalizer(&poudriereBulkBuild, myFinalizerName)
			if err := r.Update(ctx, &poudriereBulkBuild); err != nil {
				return ctrl.Result{}, err
			}
		}

		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}

	listPath := fmt.Sprintf("/usr/local/etc/poudriere.d/%s.list", poudriereBulkBuild.Name)

	if _, err := os.Stat(listPath); !errors.Is(err, os.ErrNotExist) {
		f, err := os.Open(listPath)
		if err != nil {
			return ctrl.Result{}, nil
		}
		defer f.Close()

		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return ctrl.Result{}, nil
		}

		poudriereBulkBuild.Status.Hash = fmt.Sprintf("%x", h.Sum(nil))

		if err := r.Status().Update(ctx, &poudriereBulkBuild); err != nil {
			log.Error(err, "unable to update PoudriereBulkBuild status")
			return ctrl.Result{}, err
		}
	}

	var b bytes.Buffer
	_, err := b.WriteString(strings.Join(poudriereBulkBuild.Spec.Ports, "\n"))
	if err != nil {
		return ctrl.Result{}, err
	}

	_, err = b.WriteString("\n")
	if err != nil {
		return ctrl.Result{}, err
	}

	h := sha256.New()
	hash := fmt.Sprintf("%x", h.Sum(b.Bytes()))
	if hash != poudriereBulkBuild.Status.Hash {
		f, err := os.Create(listPath)
		if err != nil {
			return ctrl.Result{}, nil
		}
		defer f.Close()

		_, err = f.WriteString(strings.Join(poudriereBulkBuild.Spec.Ports, "\n"))
		if err != nil {
			return ctrl.Result{}, nil
		}

		_, err = f.WriteString("\n")
		if err != nil {
			return ctrl.Result{}, nil
		}

		portshakerCmd := exec.Command("/usr/local/bin/portshaker")
		var portshakerOut bytes.Buffer
		var portshakerStderr bytes.Buffer
		portshakerCmd.Stdout = &portshakerOut
		portshakerCmd.Stderr = &portshakerStderr
		err = portshakerCmd.Run()
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("portshaker failed: %w: %s", err, &portshakerStderr)
		}

		cmd := exec.Command("/usr/local/bin/poudriere", "bulk", "-f", listPath, "-p", poudriereBulkBuild.Spec.Tree, "-j", poudriereBulkBuild.Spec.Jail, "-J", "2")

		var out bytes.Buffer
		var stderr bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &stderr

		err = cmd.Run()
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to execute 'bulk': %w: %s", err, stderr.String())
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PoudriereBulkBuildReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&freebsdv1.PoudriereBulkBuild{}).
		Complete(r)
}

func (r *PoudriereBulkBuildReconciler) deleteExternalResources(bulk *freebsdv1.PoudriereBulkBuild) error {
	listPath := fmt.Sprintf("/usr/local/etc/poudriere.d/%s.list", bulk.Name)
	err := os.Remove(listPath)
	return err
}
