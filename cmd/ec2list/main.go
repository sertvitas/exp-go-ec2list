package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/sertvitas/exp-go-ec2list/helper"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/jedib0t/go-pretty/v6/table"
)

const (
	apiTimeout              = 30 * time.Second
	describeServicesBatch   = 10
	deploymentStatusPrimary = "PRIMARY"
	excludedImageSubstring  = "nginx"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: ec2list <partial-cluster-name>")
	}

	partialClusterName := os.Args[1]
	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("unable to load SDK config, %v", err)
	}

	ecsClient := ecs.NewFromConfig(cfg)
	clusterArns, err := listMatchingClusters(ctx, ecsClient, partialClusterName)
	if err != nil {
		log.Fatalf("failed to list clusters, %v", err)
	}

	if len(clusterArns) == 0 {
		fmt.Printf("No matching clusters found for filter: %s\n", partialClusterName)
		return
	}

	rows, warnings := collectRows(ctx, ecsClient, clusterArns)
	renderTable(rows)

	if len(warnings) == 0 {
		return
	}

	log.Printf("completed with %d non-fatal issue(s)", len(warnings))
	for _, warn := range warnings {
		log.Printf("warning: %v", warn)
	}
}

// listMatchingClusters returns cluster ARNs that include the user filter.
func listMatchingClusters(ctx context.Context, client *ecs.Client, partialClusterName string) ([]string, error) {
	paginator := ecs.NewListClustersPaginator(client, &ecs.ListClustersInput{})
	clusterArns := make([]string, 0)

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, clusterArn := range output.ClusterArns {
			if strings.Contains(clusterArn, partialClusterName) {
				clusterArns = append(clusterArns, clusterArn)
			}
		}
	}

	return clusterArns, nil
}

// collectRows resolves cluster/service/deployment details into table rows.
func collectRows(ctx context.Context, client *ecs.Client, clusterArns []string) ([]table.Row, []error) {
	rows := make([]table.Row, 0)
	warnings := make([]error, 0)
	imageCache := make(map[string][]string)

	for _, clusterArn := range clusterArns {
		serviceArns, err := listServicesForCluster(ctx, client, clusterArn)
		if err != nil {
			warnings = append(warnings, fmt.Errorf("cluster %s list services: %w", clusterArn, err))
			continue
		}

		if len(serviceArns) == 0 {
			continue
		}

		services, batchWarnings, err := describeServicesForCluster(ctx, client, clusterArn, serviceArns)
		warnings = append(warnings, batchWarnings...)
		if err != nil {
			warnings = append(warnings, fmt.Errorf("cluster %s describe services: %w", clusterArn, err))
			continue
		}

		for _, service := range services {
			deployment, ok := primaryOrFirstDeployment(service.Deployments)
			if !ok {
				continue
			}

			if deployment.TaskDefinition == nil || *deployment.TaskDefinition == "" {
				warnings = append(warnings, fmt.Errorf("cluster %s service %s has no task definition", clusterArn, safeString(service.ServiceArn)))
				continue
			}

			images, err := taskDefinitionImages(ctx, client, *deployment.TaskDefinition, imageCache)
			if err != nil {
				warnings = append(warnings, fmt.Errorf("describe task definition %s: %w", *deployment.TaskDefinition, err))
				continue
			}

			rows = append(rows, table.Row{
				helper.ExtractName(clusterArn),
				helper.ExtractName(*deployment.TaskDefinition),
				deployment.DesiredCount,
				deployment.PendingCount,
				deployment.RunningCount,
				strings.Join(images, ", "),
			})
		}
	}

	return rows, warnings
}

// listServicesForCluster retrieves all service ARNs in a cluster using pagination.
func listServicesForCluster(ctx context.Context, client *ecs.Client, clusterArn string) ([]string, error) {
	paginator := ecs.NewListServicesPaginator(client, &ecs.ListServicesInput{Cluster: &clusterArn})
	serviceArns := make([]string, 0)

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		serviceArns = append(serviceArns, output.ServiceArns...)
	}

	return serviceArns, nil
}

// describeServicesForCluster batches DescribeServices requests to AWS limits.
func describeServicesForCluster(ctx context.Context, client *ecs.Client, clusterArn string, serviceArns []string) ([]types.Service, []error, error) {
	services := make([]types.Service, 0, len(serviceArns))
	warnings := make([]error, 0)

	for start := 0; start < len(serviceArns); start += describeServicesBatch {
		end := start + describeServicesBatch
		if end > len(serviceArns) {
			end = len(serviceArns)
		}

		output, err := client.DescribeServices(ctx, &ecs.DescribeServicesInput{
			Cluster:  &clusterArn,
			Services: serviceArns[start:end],
		})
		if err != nil {
			return nil, warnings, err
		}

		for _, failure := range output.Failures {
			warnings = append(warnings, fmt.Errorf(
				"cluster %s describe service failure arn=%s reason=%s",
				clusterArn,
				safeString(failure.Arn),
				safeString(failure.Reason),
			))
		}

		services = append(services, output.Services...)
	}

	return services, warnings, nil
}

// taskDefinitionImages returns non-excluded image names, with per-run caching.
func taskDefinitionImages(ctx context.Context, client *ecs.Client, taskDefinitionArn string, cache map[string][]string) ([]string, error) {
	if cached, ok := cache[taskDefinitionArn]; ok {
		return cached, nil
	}

	taskDefinitionOutput, err := client.DescribeTaskDefinition(ctx, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: &taskDefinitionArn,
	})
	if err != nil {
		return nil, err
	}
	if taskDefinitionOutput.TaskDefinition == nil {
		return nil, fmt.Errorf("task definition response is empty for %s", taskDefinitionArn)
	}

	images := make([]string, 0)
	for _, containerDef := range taskDefinitionOutput.TaskDefinition.ContainerDefinitions {
		if containerDef.Image == nil || *containerDef.Image == "" {
			continue
		}

		containerImage := helper.ExtractImageName(*containerDef.Image)
		if strings.Contains(containerImage, excludedImageSubstring) {
			continue
		}

		images = append(images, containerImage)
	}

	cache[taskDefinitionArn] = images
	return images, nil
}

// primaryOrFirstDeployment prefers PRIMARY deployment and falls back to first.
func primaryOrFirstDeployment(deployments []types.Deployment) (types.Deployment, bool) {
	for _, deployment := range deployments {
		if deployment.Status != nil && *deployment.Status == deploymentStatusPrimary {
			return deployment, true
		}
	}

	if len(deployments) == 0 {
		return types.Deployment{}, false
	}

	return deployments[0], true
}

func renderTable(rows []table.Row) {
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"Cluster Name", "Task Definition", "Desired Count", "Pending Count", "Running Count", "Container Image"})

	for _, row := range rows {
		t.AppendRow(row)
	}

	t.Render()
}

func safeString(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}
