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
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr"
	freebsdv1 "github.com/xaque208/freebsd-controller/api/v1"
)

// PoudrierePortsTreeReconciler reconciles a PoudrierePortsTree object
type PoudrierePortsTreeReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=freebsd.znet,resources=poudriereportstrees,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=freebsd.znet,resources=poudriereportstrees/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=freebsd.znet,resources=poudriereportstrees/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the PoudrierePortsTree object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.2/pkg/reconcile
func (r *PoudrierePortsTreeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var poudrierePortsTree freebsdv1.PoudrierePortsTree
	if err := r.Get(ctx, req.NamespacedName, &poudrierePortsTree); err != nil {
		log.Error(err, "unable to fetch PoudrierePortsTree")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	myFinalizerName := "poudriereportstree.freebsd.znet/finalizer"

	if poudrierePortsTree.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !controllerutil.ContainsFinalizer(&poudrierePortsTree, myFinalizerName) {
			controllerutil.AddFinalizer(&poudrierePortsTree, myFinalizerName)
			if err := r.Update(ctx, &poudrierePortsTree); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(&poudrierePortsTree, myFinalizerName) {
			// our finalizer is present, so lets handle any external dependency
			if err := r.deleteExternalResources(&poudrierePortsTree); err != nil {
				// if fail to delete the external dependency here, return with error
				// so that it can be retried
				return ctrl.Result{}, err
			}

			// remove our finalizer from the list and update it.
			controllerutil.RemoveFinalizer(&poudrierePortsTree, myFinalizerName)
			if err := r.Update(ctx, &poudrierePortsTree); err != nil {
				return ctrl.Result{}, err
			}
		}

		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}

	cmd := exec.Command("/usr/local/bin/poudriere", "ports", "-l")

	out, err := cmd.Output()
	if err != nil {
		log.Error(err, "unable to read poudriere command output 'ports -l'")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	reader := bytes.NewReader(out)
	status, err := readPoudrierePortsStatus(reader, log, req)
	if err != nil {
		log.Error(err, "unable to read poudriere jail stats")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	poudrierePortsTree.Status = status

	if err := r.Status().Update(ctx, &poudrierePortsTree); err != nil {
		log.Error(err, "unable to update PoudriereJail status")
		return ctrl.Result{}, err
	}

	if status.CreationDate == "" {
		cmd := exec.Command("/usr/local/bin/poudriere", "ports", "-c", "-p", req.Name, "-m", poudrierePortsTree.Spec.FetchMethod)

		var out bytes.Buffer
		var stderr bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &stderr

		err := cmd.Run()
		if err != nil {
			log.Error(err, fmt.Sprintf("failed to execute'jail -c': %s", stderr.String()))
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PoudrierePortsTreeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&freebsdv1.PoudrierePortsTree{}).
		Complete(r)
}

func (r *PoudrierePortsTreeReconciler) deleteExternalResources(tree *freebsdv1.PoudrierePortsTree) error {
	cmd := exec.Command("/usr/local/bin/poudriere", "ports", "-d", "-p", tree.Name)
	_, err := cmd.Output()
	if err != nil {
		return client.IgnoreNotFound(err)
	}

	return nil
}

func readPoudrierePortsStatus(r io.Reader, log logr.Logger, req ctrl.Request) (freebsdv1.PoudrierePortsTreeStatus, error) {
	var status freebsdv1.PoudrierePortsTreeStatus

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)

		if len(parts) != 5 {
			log.Info(fmt.Sprintf("skipping due to !=5 parts: %d: %s", len(parts), parts))
			continue
		}

		if parts[0] != req.Name {
			log.Info(fmt.Sprintf("skipping due to name not match %s", parts[0]))
			continue
		}

		status.FetchMethod = parts[1]
		status.CreationDate = parts[2]
		status.CreationTime = parts[3]
		status.Mountpoint = parts[4]

		return status, nil
	}

	if err := scanner.Err(); err != nil {
		return freebsdv1.PoudrierePortsTreeStatus{}, err
	}

	return status, nil
}
