/*

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
	aws "github.com/aws/aws-sdk-go/aws"
	awserr "github.com/aws/aws-sdk-go/aws/awserr"
	session "github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/go-logr/logr"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	storagev1 "bucket-controller/api/v1"
)

var myFinalizerName = "bucket.finalizers.storage.k8s.honestbee.io"

// BucketReconciler reconciles a Bucket object
type BucketReconciler struct {
	client.Client
	Log logr.Logger
}

// +kubebuilder:rbac:groups=storage.k8s.honestbee.io,resources=buckets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=storage.k8s.honestbee.io,resources=buckets/status,verbs=get;list;watch;create;update;patch;delete

func ignoreNotFound(err error) error {
	if apierrs.IsNotFound(err) {
		return nil
	}
	return err
}

func (r *BucketReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("bucket", req.NamespacedName)
	b := storagev1.Bucket{}

	if err := r.Get(ctx, req.NamespacedName, &b); err != nil {
		log.Error(err, "Unable to fetch Bucket")
		return ctrl.Result{}, ignoreNotFound(err)
	}

	var s3Client = s3.New(session.New(aws.NewConfig()))

	if b.ObjectMeta.DeletionTimestamp.IsZero() {
		if !containsString(b.ObjectMeta.Finalizers, myFinalizerName) {
			b.ObjectMeta.Finalizers = append(b.ObjectMeta.Finalizers, myFinalizerName)
			if err := r.Update(context.Background(), &b); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		if containsString(b.ObjectMeta.Finalizers, myFinalizerName) {
			if err := r.DeleteBucket(&b); err != nil {
				return ctrl.Result{}, err
			}
			b.ObjectMeta.Finalizers = removeString(b.ObjectMeta.Finalizers, myFinalizerName)
			if err := r.Update(context.Background(), &b); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, nil
	}

	if b.Status.BucketCreated != true {
		if _, err := s3Client.CreateBucket(&s3.CreateBucketInput{Bucket: aws.String(b.Spec.BucketName), CreateBucketConfiguration: &s3.CreateBucketConfiguration{LocationConstraint: aws.String(b.Spec.Region)}}); err != nil {
			if err.(awserr.Error).Code() == "BucketAlreadyOwnedByYou" {
				return ctrl.Result{}, nil
			}
			log.Error(err, "Unable to create Bucket")
			return ctrl.Result{}, err
		}

		b.Status.BucketCreated = true

		if err := r.Status().Update(ctx, &b); err != nil {
			log.Error(err, "Unable to update Status")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *BucketReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&storagev1.Bucket{}).
		Complete(r)
}

func (r *BucketReconciler) DeleteBucket(b *storagev1.Bucket) error {
	var s3Client = s3.New(session.New(aws.NewConfig()))

	if b.Spec.ForceDelete {
		if o, err := s3Client.ListObjects(&s3.ListObjectsInput{Bucket: aws.String(b.Spec.BucketName)}); err != nil {
			if err.(awserr.Error).Code() == "NoSuchBucket" {
				return nil
			}
			return err
		} else {
			for i := 0; i < len(o.Contents); i++ {
				obj := o.Contents[i]
				if _, err = s3Client.DeleteObject(&s3.DeleteObjectInput{Bucket: aws.String(b.Spec.BucketName), Key: obj.Key}); err != nil {
					return err
				}
			}
		}
	}

	_, err := s3Client.DeleteBucket(&s3.DeleteBucketInput{Bucket: &b.Spec.BucketName})

	if err != nil {
		if err.(awserr.Error).Code() == "NoSuchBucket" ||  err.(awserr.Error).Code() == "BucketNotEmpty" {
			return nil
		}
		return err
	}

	return nil
}
