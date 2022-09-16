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
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr"
	freebsdv1 "github.com/xaque208/freebsd_controller/api/v1"
)

// PoudriereJailReconciler reconciles a PoudriereJail object
type PoudriereJailReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=freebsd.znet,resources=poudrierejails,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=freebsd.znet,resources=poudrierejails/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=freebsd.znet,resources=poudrierejails/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the PoudriereJail object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.2/pkg/reconcile
func (r *PoudriereJailReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	err := nodeLabelMatch(ctx, r, req, poudriereLabelGate)
	if err != nil {
		log.Error(err, "node labels did not match")
		return ctrl.Result{}, nil
	}

	var poudriereJail freebsdv1.PoudriereJail
	if err := r.Get(ctx, req.NamespacedName, &poudriereJail); err != nil {
		log.Error(err, "unable to fetch PoudriereJail")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	myFinalizerName := "poudrierejail.freebsd.znet/finalizer"

	if poudriereJail.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !controllerutil.ContainsFinalizer(&poudriereJail, myFinalizerName) {
			controllerutil.AddFinalizer(&poudriereJail, myFinalizerName)
			if err := r.Update(ctx, &poudriereJail); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(&poudriereJail, myFinalizerName) {
			// our finalizer is present, so lets handle any external dependency
			if err := r.deleteExternalResources(&poudriereJail); err != nil {
				// if fail to delete the external dependency here, return with error
				// so that it can be retried
				return ctrl.Result{}, err
			}

			// remove our finalizer from the list and update it.
			controllerutil.RemoveFinalizer(&poudriereJail, myFinalizerName)
			if err := r.Update(ctx, &poudriereJail); err != nil {
				return ctrl.Result{}, err
			}
		}

		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}

	makeoptsPath := fmt.Sprintf("/usr/local/etc/poudriere.d/%s-make.conf", poudriereJail.Name)

	var b bytes.Buffer
	_, err = b.WriteString(poudriereJail.Spec.Makeopts)
	if err != nil {
		return ctrl.Result{}, err
	}

	h := sha256.New()
	hash := fmt.Sprintf("%x", h.Sum(b.Bytes()))
	if hash != poudriereJail.Status.MakeoptsHash {
		f, createErr := os.Create(makeoptsPath)
		if createErr != nil {
			return ctrl.Result{}, createErr
		}
		defer f.Close()

		_, err = f.WriteString(poudriereJail.Spec.Makeopts)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	cmd := exec.Command("/usr/local/bin/poudriere", "jail", "-l")

	out, err := cmd.Output()
	if err != nil {
		log.Error(err, "unable to read poudriere command output 'jail -l'")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	reader := bytes.NewReader(out)
	status, err := readPoudriereJailStatus(reader, log, req)
	if err != nil {
		log.Error(err, "unable to read poudriere jail stats")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	poudriereJail.Status = status

	if _, err := os.Stat(makeoptsPath); !errors.Is(err, os.ErrNotExist) {
		f, err := os.Open(makeoptsPath)
		if err != nil {
			return ctrl.Result{}, nil
		}
		defer f.Close()

		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return ctrl.Result{}, nil
		}

		poudriereJail.Status.MakeoptsHash = fmt.Sprintf("%x", h.Sum(nil))
	}

	if err := r.Status().Update(ctx, &poudriereJail); err != nil {
		log.Error(err, "unable to update PoudriereJail status")
		return ctrl.Result{}, err
	}

	if status.CreationDate == "" {
		// poudriere jail -c -j poudrierejail-testing3 -v release/13.1.0 -m git+https
		cmd := exec.Command("/usr/local/bin/poudriere", "jail", "-c", "-j", req.Name, "-v", poudriereJail.Spec.Version, "-m", "http")

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
func (r *PoudriereJailReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&freebsdv1.PoudriereJail{}).
		Complete(r)
}

func readPoudriereJailStatus(r io.Reader, log logr.Logger, req ctrl.Request) (freebsdv1.PoudriereJailStatus, error) {
	var status freebsdv1.PoudriereJailStatus

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)

		if len(parts) != 7 {
			log.Info(fmt.Sprintf("skipping due to !=6 parts: %d: %s", len(parts), parts))
			continue
		}

		if parts[0] != req.Name {
			log.Info(fmt.Sprintf("skipping due to name not match %s", parts[0]))
			continue
		}

		status.Version = parts[1]
		status.Architecture = parts[2]
		status.FetchMethod = parts[3]
		status.CreationDate = parts[4]
		status.CreationTime = parts[5]
		status.Mountpoint = parts[6]
		status.Ready = true
	}

	if err := scanner.Err(); err != nil {
		return freebsdv1.PoudriereJailStatus{}, err
	}

	return status, nil
}

func (r *PoudriereJailReconciler) deleteExternalResources(jail *freebsdv1.PoudriereJail) error {
	cmd := exec.Command("/usr/local/bin/poudriere", "jail", "-d", "-j", jail.Name)
	_, err := cmd.Output()
	if err != nil {
		return client.IgnoreNotFound(err)
	}

	return nil
}
