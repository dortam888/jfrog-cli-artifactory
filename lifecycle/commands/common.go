package commands

import (
	"fmt"
	"github.com/jfrog/jfrog-cli-core/v2/artifactory/utils"
	"github.com/jfrog/jfrog-cli-core/v2/common/spec"
	"github.com/jfrog/jfrog-cli-core/v2/utils/config"
	"github.com/jfrog/jfrog-client-go/lifecycle"
	"github.com/jfrog/jfrog-client-go/lifecycle/services"
	clientUtils "github.com/jfrog/jfrog-client-go/utils"
	"github.com/jfrog/jfrog-client-go/utils/distribution"
)

const (
	rbV2manifestName                                      = "release-bundle.json.evd"
	releaseBundlesV2                                      = "release-bundles-v2"
	minimalLifecycleArtifactoryVersion                    = "7.63.2"
	minArtifactoryVersionForMultiSourceAndPackagesSupport = "7.114.0"
)

type releaseBundleCmd struct {
	serverDetails        *config.ServerDetails
	releaseBundleName    string
	releaseBundleVersion string
	sync                 bool
	rbProjectKey         string
}

func (rbc *releaseBundleCmd) getPrerequisites() (servicesManager *lifecycle.LifecycleServicesManager,
	rbDetails services.ReleaseBundleDetails, queryParams services.CommonOptionalQueryParams, err error) {
	return rbc.initPrerequisites()
}

func (rbp *ReleaseBundlePromoteCommand) getPromotionPrerequisites() (servicesManager *lifecycle.LifecycleServicesManager,
	rbDetails services.ReleaseBundleDetails, queryParams services.CommonOptionalQueryParams, err error) {
	servicesManager, rbDetails, queryParams, err = rbp.initPrerequisites()
	queryParams.PromotionType = rbp.promotionType
	return servicesManager, rbDetails, queryParams, err
}

func (rbc *releaseBundleCmd) initPrerequisites() (servicesManager *lifecycle.LifecycleServicesManager,
	rbDetails services.ReleaseBundleDetails, queryParams services.CommonOptionalQueryParams, err error) {
	servicesManager, err = utils.CreateLifecycleServiceManager(rbc.serverDetails, false)
	if err != nil {
		return
	}
	rbDetails = services.ReleaseBundleDetails{
		ReleaseBundleName:    rbc.releaseBundleName,
		ReleaseBundleVersion: rbc.releaseBundleVersion,
	}
	queryParams = services.CommonOptionalQueryParams{
		ProjectKey: rbc.rbProjectKey,
		Async:      !rbc.sync,
	}

	return
}

func validateArtifactoryVersion(serverDetails *config.ServerDetails, minVersion string) error {
	rtServiceManager, err := utils.CreateServiceManager(serverDetails, 3, 0, false)
	if err != nil {
		return err
	}

	versionStr, err := rtServiceManager.GetVersion()
	if err != nil {
		return err
	}

	return clientUtils.ValidateMinimumVersion(clientUtils.Artifactory, versionStr, minVersion)
}

func validateArtifactoryVersionSupported(serverDetails *config.ServerDetails) error {
	return validateArtifactoryVersion(serverDetails, minimalLifecycleArtifactoryVersion)
}

func ValidateFeatureSupportedVersion(serverDetails *config.ServerDetails, minCommandVersion string) error {
	return validateArtifactoryVersion(serverDetails, minCommandVersion)
}

// If distribution rules are empty, distribute to all edges.
func getAggregatedDistRules(distributionRules *spec.DistributionRules) (aggregatedRules []*distribution.DistributionCommonParams) {
	if isDistributionRulesEmpty(distributionRules) {
		aggregatedRules = append(aggregatedRules, &distribution.DistributionCommonParams{SiteName: "*"})
	} else {
		for _, rules := range distributionRules.DistributionRules {
			aggregatedRules = append(aggregatedRules, rules.ToDistributionCommonParams())
		}
	}
	return
}

func isDistributionRulesEmpty(distributionRules *spec.DistributionRules) bool {
	return distributionRules == nil ||
		len(distributionRules.DistributionRules) == 0 ||
		len(distributionRules.DistributionRules) == 1 && distributionRules.DistributionRules[0].IsEmpty()
}

func buildRepoKey(project string) string {
	if project == "" || project == "default" {
		return releaseBundlesV2
	}
	return fmt.Sprintf("%s-%s", project, releaseBundlesV2)
}

func buildManifestPath(projectKey, name, version string) string {
	return fmt.Sprintf("%s/%s/%s/%s", buildRepoKey(projectKey), name, version, rbV2manifestName)
}
