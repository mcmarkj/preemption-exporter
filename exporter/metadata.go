package exporter

import (
	"context"
	"errors"
	"k8s.io/client-go/rest"

	//"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"time"

	_ "flag"
	_ "path/filepath"

	"github.com/prometheus/client_golang/prometheus"

	log "github.com/sirupsen/logrus"

	//k8_err "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	_ "k8s.io/client-go/tools/clientcmd"
	_ "k8s.io/client-go/util/homedir"
)

type terminationExporter struct {
	httpCli              *http.Client
	metadataEndpoint     string
	scrapeSuccessful     *prometheus.Desc
	terminationIndicator *prometheus.Desc
	terminatedJobs 		 *prometheus.Desc
}

func NewPreemptionExporter(me string) *terminationExporter {
	netTransport := &http.Transport{
		Dial: (&net.Dialer{
			Timeout: 5 * time.Second,
		}).Dial,
		TLSHandshakeTimeout: 5 * time.Second,
	}
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: netTransport,
	}
	return &terminationExporter{
		httpCli:              client,
		metadataEndpoint:     me,
		scrapeSuccessful:     prometheus.NewDesc("gcp_instance_metadata_service_available", "Metadata service available", []string{"instance_id", "instance_name"}, nil),
		terminationIndicator: prometheus.NewDesc("gcp_instance_termination_imminent", "Instance is about to be terminated", []string{"instance_id", "instance_name"}, nil),
		terminatedJobs: 	  prometheus.NewDesc("jenkins_preempted_jobs", "List of jobs that were running on a preempted node", []string{"instance_name", "job_name", "pod_name"}, nil),
	}
}

func (c *terminationExporter) get(path string) (string, error) {
	req, err := http.NewRequest("GET", c.metadataEndpoint+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := c.httpCli.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode == http.StatusNotFound {
		return "", errors.New("endpoint not fount")
	}
	return string(body), nil
}

func (c *terminationExporter) GetJobs(instance string, preemptedValue float64, ch chan<- prometheus.Metric){


	// creates the in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}
	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}


	//var kubeconfig *string
	//if home := homedir.HomeDir(); home != "" {
	//	kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	//} else {
	//	kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	//}
	//flag.Parse()

	// use the current context in kubeconfig
	//config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	//if err != nil {
	//	panic(err.Error())
	//}

	// create the clientset
	//clientset, err := kubernetes.NewForConfig(config)
	//if err != nil {
	//	panic(err.Error())
	//}

	regex := *regexp.MustCompile(`^job\/(?P<job>.*)\/job\/(?P<branch>.*)\/(?P<build>.*)\/$`)

	pods, err := clientset.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{})
	for _, podInfo := range (*pods).Items {

		//instance = "gke-dev-cluster-jenkins-executors-bfe2764a-0m8f"
		if podInfo.Spec.NodeName == instance {
			jenkinsRunurl := podInfo.ObjectMeta.Annotations["runUrl"]
			if jenkinsRunurl != "" {
				rs := regex.FindStringSubmatch(jenkinsRunurl)
				runurl := rs[1]+"/"+rs[2]+"/"+rs[3]
				ch <- prometheus.MustNewConstMetric(c.terminatedJobs, prometheus.GaugeValue, preemptedValue, instance, runurl, podInfo.Name)
			}
		}
	}
}

func (c *terminationExporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.scrapeSuccessful
	ch <- c.terminationIndicator
	ch <- c.terminatedJobs
}

func (c *terminationExporter) Collect(ch chan<- prometheus.Metric) {
	var preemptedValue float64
	log.Info("Fetching termination data from metadata-service")
	instanceID, err := c.get("id")
	if err != nil {
		log.Errorf("couldn't parse instance id from metadata: %s", err.Error())
		ch <- prometheus.MustNewConstMetric(c.scrapeSuccessful, prometheus.GaugeValue, 0, "none", "none")
		return
	}
	instanceName, err := c.get("name")
	if err != nil {
		log.Errorf("couldn't parse instance name from metadata: %s", err.Error())
		ch <- prometheus.MustNewConstMetric(c.scrapeSuccessful, prometheus.GaugeValue, 0, instanceID, "none")
		return
	}
	preempted, err := c.get("preempted")
	if err != nil {
		log.Errorf("Failed to fetch data from metadata service: %s", err)
		ch <- prometheus.MustNewConstMetric(c.scrapeSuccessful, prometheus.GaugeValue, 0, instanceID, instanceName)
		return
	}
	ch <- prometheus.MustNewConstMetric(c.scrapeSuccessful, prometheus.GaugeValue, 1, instanceID, instanceName)
	log.Infof("instance endpoint available, will be preempted: %v", preempted)
	if isPreempted, _ := strconv.ParseBool(preempted); isPreempted {
		preemptedValue = 1.0
	}
	c.GetJobs(instanceName, preemptedValue, ch)
	ch <- prometheus.MustNewConstMetric(c.terminationIndicator, prometheus.GaugeValue, preemptedValue, instanceID, instanceName)
}
