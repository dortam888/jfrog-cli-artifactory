package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jfrog/gofrog/version"
	"github.com/jfrog/jfrog-client-go/utils/log"
	"strconv"
	"strings"

	"github.com/jfrog/jfrog-cli-core/v2/artifactory/commands/utils"
	rtUtils "github.com/jfrog/jfrog-cli-core/v2/artifactory/utils"
	"github.com/jfrog/jfrog-cli-core/v2/utils/config"
	"github.com/jfrog/jfrog-cli-core/v2/utils/ioutils"
	"github.com/jfrog/jfrog-client-go/artifactory"
	"github.com/jfrog/jfrog-client-go/artifactory/services"
	"github.com/jfrog/jfrog-client-go/utils/errorutils"
)

const (
	// The actual field in the repository configuration is an array (plural) but in practice only one environment is allowed.
	// This is why the question differs from the repository configuration.
	environmentsKey = "environments"
)

type RepoCommand struct {
	serverDetails *config.ServerDetails
	templatePath  string
	vars          string
}

func (rc *RepoCommand) Vars() string {
	return rc.vars
}

func (rc *RepoCommand) TemplatePath() string {
	return rc.templatePath
}

type repoCreateUpdateHandler interface {
	Execute(repoConfigMaps []map[string]interface{}, servicesManager artifactory.ArtifactoryServicesManager, isUpdate bool) error
}

type (
	MultipleRepositoryHandler struct{}
	SingleRepositoryHandler   struct{}
)

func (rc *RepoCommand) PerformRepoCmd(isUpdate bool) (err error) {
	configs, err := utils.ConvertTemplateToMaps(rc)
	if err != nil {
		return err
	}

	var (
		repoConfigMaps []map[string]interface{}
		strategy       repoCreateUpdateHandler
	)

	switch configType := configs.(type) {
	case []map[string]interface{}:
		repoConfigMaps = configType
		strategy = &MultipleRepositoryHandler{}
	case map[string]interface{}:
		repoConfigMaps = []map[string]interface{}{configType}
		strategy = &SingleRepositoryHandler{}
	default:
		return fmt.Errorf("unexpected repository configuration type: %T", configType)
	}

	var missingKeys []string
	for _, repoConfigMap := range repoConfigMaps {
		if key, ok := repoConfigMap["key"]; !ok || key == "" {
			missingKeys = append(missingKeys, fmt.Sprintf("%v\n", repoConfigMap))
		}
	}

	if len(missingKeys) > 0 {
		return fmt.Errorf("'key' is missing in the following configs\n: %v", missingKeys)
	}

	servicesManager, err := rtUtils.CreateServiceManager(rc.serverDetails, -1, 0, false)
	if err != nil {
		return err
	}

	return strategy.Execute(repoConfigMaps, servicesManager, isUpdate)
}

func (m *MultipleRepositoryHandler) Execute(repoConfigMaps []map[string]interface{}, servicesManager artifactory.ArtifactoryServicesManager, isUpdate bool) error {
	content, err := json.Marshal(repoConfigMaps)
	if err != nil {
		return err
	}
	return multipleRepoHandler(servicesManager, content, isUpdate)
}

func (s *SingleRepositoryHandler) Execute(repoConfigMaps []map[string]interface{}, servicesManager artifactory.ArtifactoryServicesManager, isUpdate bool) error {
	// Go over the confMap and write the values with the correct type using the writersMap
	for _, repoConfigMap := range repoConfigMaps {
		for key, value := range repoConfigMap {
			if err := utils.ValidateMapEntry(key, value, writersMap); err != nil {
				return err
			}
			if err := writersMap[key](&repoConfigMap, key, fmt.Sprint(value)); err != nil {
				return err
			}
		}

		content, err := json.Marshal(repoConfigMap)
		if err != nil {
			return err
		}

		// Rclass and packageType are mandatory keys in our templates
		// Using their values we'll pick the suitable handler from one of the handler maps to create/update a repository
		var handlerFunc func(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error
		packageType := fmt.Sprint(repoConfigMap[PackageType])
		switch repoConfigMap[Rclass] {
		case Local:
			handlerFunc = localRepoHandlers[packageType]
		case Remote:
			handlerFunc = remoteRepoHandlers[packageType]
		case Virtual:
			handlerFunc = virtualRepoHandlers[packageType]
		case Federated:
			handlerFunc = federatedRepoHandlers[packageType]
		default:
			return errorutils.CheckErrorf("unsupported rclass: %s", repoConfigMap[Rclass])
		}
		if handlerFunc == nil {
			return errors.New("unsupported package type: " + packageType)
		}

		if err := handlerFunc(servicesManager, content, isUpdate); err != nil {
			return err
		}
	}
	return nil
}

func multipleRepoHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) (err error) {
	artifactoryVersion, err := servicesManager.GetVersion()
	if err != nil {
		return errorutils.CheckErrorf("failed to get Artifactory rtVersion: %s", err.Error())
	}
	rtVersion := version.NewVersion(artifactoryVersion)
	if isUpdate {
		if !rtVersion.AtLeast("7.104.2") {
			return errorutils.CheckErrorf("bulk repository updation is supported from Artifactory rtVersion 7.104.2, current rtVersion: %v", artifactoryVersion)
		}
	} else {
		if !rtVersion.AtLeast("7.84.3") {
			return errorutils.CheckErrorf("bulk repository creation is supported from Artifactory rtVersion 7.84.3, current rtVersion: %v", artifactoryVersion)
		}
	}

	log.Debug("creating/updating repositories in batch...")

	err = servicesManager.CreateUpdateRepositoriesInBatch(jsonConfig, isUpdate)
	if err != nil {
		return err
	}

	log.Info("Successfully created/updated the repositories")

	return nil
}

var writersMap = map[string]ioutils.AnswerWriter{
	Key:                               ioutils.WriteStringAnswer,
	Rclass:                            ioutils.WriteStringAnswer,
	PackageType:                       ioutils.WriteStringAnswer,
	MandatoryUrl:                      ioutils.WriteStringAnswer,
	Url:                               ioutils.WriteStringAnswer,
	Description:                       ioutils.WriteStringAnswer,
	Notes:                             ioutils.WriteStringAnswer,
	IncludePatterns:                   ioutils.WriteStringAnswer,
	ExcludePatterns:                   ioutils.WriteStringAnswer,
	RepoLayoutRef:                     ioutils.WriteStringAnswer,
	ProjectKey:                        ioutils.WriteStringAnswer,
	environmentsKey:                   ioutils.WriteStringArrayAnswer,
	HandleReleases:                    ioutils.WriteBoolAnswer,
	HandleSnapshots:                   ioutils.WriteBoolAnswer,
	MaxUniqueSnapshots:                ioutils.WriteIntAnswer,
	SuppressPomConsistencyChecks:      ioutils.WriteBoolAnswer,
	BlackedOut:                        ioutils.WriteBoolAnswer,
	DownloadRedirect:                  ioutils.WriteBoolAnswer,
	PriorityResolution:                ioutils.WriteBoolAnswer,
	CdnRedirect:                       ioutils.WriteBoolAnswer,
	BlockPushingSchema1:               ioutils.WriteBoolAnswer,
	DebianTrivialLayout:               ioutils.WriteBoolAnswer,
	ExternalDependenciesEnabled:       ioutils.WriteBoolAnswer,
	ExternalDependenciesPatterns:      ioutils.WriteStringArrayAnswer,
	ChecksumPolicyType:                ioutils.WriteStringAnswer,
	MaxUniqueTags:                     ioutils.WriteIntAnswer,
	SnapshotVersionBehavior:           ioutils.WriteStringAnswer,
	XrayIndex:                         ioutils.WriteBoolAnswer,
	PropertySets:                      ioutils.WriteStringArrayAnswer,
	ArchiveBrowsingEnabled:            ioutils.WriteBoolAnswer,
	CalculateYumMetadata:              ioutils.WriteBoolAnswer,
	YumRootDepth:                      ioutils.WriteIntAnswer,
	DockerApiVersion:                  ioutils.WriteStringAnswer,
	EnableFileListsIndexing:           ioutils.WriteBoolAnswer,
	OptionalIndexCompressionFormats:   ioutils.WriteStringArrayAnswer,
	Username:                          ioutils.WriteStringAnswer,
	Password:                          ioutils.WriteStringAnswer,
	Proxy:                             ioutils.WriteStringAnswer,
	RemoteRepoChecksumPolicyType:      ioutils.WriteStringAnswer,
	HardFail:                          ioutils.WriteBoolAnswer,
	Offline:                           ioutils.WriteBoolAnswer,
	StoreArtifactsLocally:             ioutils.WriteBoolAnswer,
	SocketTimeoutMillis:               ioutils.WriteIntAnswer,
	LocalAddress:                      ioutils.WriteStringAnswer,
	RetrievalCachePeriodSecs:          ioutils.WriteIntAnswer,
	FailedRetrievalCachePeriodSecs:    ioutils.WriteIntAnswer,
	MissedRetrievalCachePeriodSecs:    ioutils.WriteIntAnswer,
	UnusedArtifactsCleanupEnabled:     ioutils.WriteBoolAnswer,
	UnusedArtifactsCleanupPeriodHours: ioutils.WriteIntAnswer,
	AssumedOfflinePeriodSecs:          ioutils.WriteIntAnswer,
	FetchJarsEagerly:                  ioutils.WriteBoolAnswer,
	FetchSourcesEagerly:               ioutils.WriteBoolAnswer,
	ShareConfiguration:                ioutils.WriteBoolAnswer,
	SynchronizeProperties:             ioutils.WriteBoolAnswer,
	BlockMismatchingMimeTypes:         ioutils.WriteBoolAnswer,
	AllowAnyHostAuth:                  ioutils.WriteBoolAnswer,
	EnableCookieManagement:            ioutils.WriteBoolAnswer,
	BowerRegistryUrl:                  ioutils.WriteStringAnswer,
	ComposerRegistryUrl:               ioutils.WriteStringAnswer,
	PyPIRegistryUrl:                   ioutils.WriteStringAnswer,
	VcsType:                           ioutils.WriteStringAnswer,
	VcsGitProvider:                    ioutils.WriteStringAnswer,
	VcsGitDownloadUrl:                 ioutils.WriteStringAnswer,
	BypassHeadRequests:                ioutils.WriteBoolAnswer,
	ClientTlsCertificate:              ioutils.WriteStringAnswer,
	FeedContextPath:                   ioutils.WriteStringAnswer,
	DownloadContextPath:               ioutils.WriteStringAnswer,
	V3FeedUrl:                         ioutils.WriteStringAnswer,
	ContentSynchronisation:            writeContentSynchronisation,
	ListRemoteFolderItems:             ioutils.WriteBoolAnswer,
	RejectInvalidJars:                 ioutils.WriteBoolAnswer,
	PodsSpecsRepoUrl:                  ioutils.WriteStringAnswer,
	EnableTokenAuthentication:         ioutils.WriteBoolAnswer,
	Repositories:                      ioutils.WriteStringArrayAnswer,
	ArtifactoryRequestsCanRetrieveRemoteArtifacts: ioutils.WriteBoolAnswer,
	KeyPair:                              ioutils.WriteStringAnswer,
	PomRepositoryReferencesCleanupPolicy: ioutils.WriteStringAnswer,
	DefaultDeploymentRepo:                ioutils.WriteStringAnswer,
	ForceMavenAuthentication:             ioutils.WriteBoolAnswer,
	ForceNugetAuthentication:             ioutils.WriteBoolAnswer,
	ExternalDependenciesRemoteRepo:       ioutils.WriteStringAnswer,
}

func writeContentSynchronisation(resultMap *map[string]interface{}, key, value string) error {
	answerArray := strings.Split(value, ",")
	if len(answerArray) != 4 {
		return errors.New("invalid value for Content Synchronisation")
	}
	var cs services.ContentSynchronisation

	enabled, err := strconv.ParseBool(answerArray[0])
	if errorutils.CheckError(err) != nil {
		return err
	}
	cs.Enabled = &enabled

	enabled, err = strconv.ParseBool(answerArray[1])
	if errorutils.CheckError(err) != nil {
		return err
	}
	cs.Statistics = &services.ContentSynchronisationStatistics{
		Enabled: &enabled,
	}

	enabled, err = strconv.ParseBool(answerArray[2])
	if errorutils.CheckError(err) != nil {
		return err
	}
	cs.Properties = &services.ContentSynchronisationProperties{
		Enabled: &enabled,
	}

	enabled, err = strconv.ParseBool(answerArray[3])
	if errorutils.CheckError(err) != nil {
		return err
	}
	cs.Source = &services.ContentSynchronisationSource{
		OriginAbsenceDetection: &enabled,
	}

	(*resultMap)[key] = cs
	return nil
}

// repoHandler is a function that gets serviceManager, JSON configuration content and a flag indicates is the operation in an update operation
// Each handler unmarshal the JSOn content into the jfrog-client's unique rclass-pkgType param struct, and run the operation service
type repoHandler func(artifactory.ArtifactoryServicesManager, []byte, bool) error

var localRepoHandlers = map[string]repoHandler{
	Maven:     localMavenHandler,
	Gradle:    localGradleHandler,
	Ivy:       localIvyHandles,
	Sbt:       localSbtHandler,
	Helm:      localHelmHandler,
	Cocoapods: localCocoapodsHandler,
	Opkg:      localOpkgHandler,
	Rpm:       localRpmHandler,
	Nuget:     localNugetHandler,
	Cran:      localCranHandler,
	Gems:      localGemsHandler,
	Npm:       localNpmHandler,
	Bower:     localBowerHandler,
	Debian:    localDebianHandler,
	Composer:  localComposerHandler,
	Pypi:      localPypiHandler,
	Docker:    localDockerHandler,
	Vagrant:   localVagrantHandler,
	Gitlfs:    localGitLfsHandler,
	Go:        localGoHandler,
	Yum:       localYumHandler,
	Conan:     localConanHandler,
	Conda:     localCondaHandler,
	Chef:      localChefHandler,
	Puppet:    localPuppetHandler,
	Alpine:    localAlpineHandler,
	Generic:   localGenericHandler,
	Swift:     localSwiftHandler,
	Terraform: localTerraformHandler,
	Cargo:     localCargoHandler,
}

func localMavenHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewMavenLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Maven(params)
	} else {
		err = servicesManager.CreateLocalRepository().Maven(params)
	}
	return err
}

func localGradleHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewGradleLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Gradle(params)
	} else {
		err = servicesManager.CreateLocalRepository().Gradle(params)
	}
	return err
}

func localIvyHandles(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewIvyLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Ivy(params)
	} else {
		err = servicesManager.CreateLocalRepository().Ivy(params)
	}
	return err
}

func localSbtHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewSbtLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Sbt(params)
	} else {
		err = servicesManager.CreateLocalRepository().Sbt(params)
	}
	return err
}

func localHelmHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewHelmLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Helm(params)
	} else {
		err = servicesManager.CreateLocalRepository().Helm(params)
	}
	return err
}

func localCocoapodsHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewCocoapodsLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Cocoapods(params)
	} else {
		err = servicesManager.CreateLocalRepository().Cocoapods(params)
	}
	return err
}

func localOpkgHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewOpkgLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Opkg(params)
	} else {
		err = servicesManager.CreateLocalRepository().Opkg(params)
	}
	return err
}

func localRpmHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewRpmLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Rpm(params)
	} else {
		err = servicesManager.CreateLocalRepository().Rpm(params)
	}
	return err
}

func localNugetHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewNugetLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Nuget(params)
	} else {
		err = servicesManager.CreateLocalRepository().Nuget(params)
	}
	return err
}

func localCranHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewCranLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Cran(params)
	} else {
		err = servicesManager.CreateLocalRepository().Cran(params)
	}
	return err
}

func localGemsHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewGemsLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Gems(params)
	} else {
		err = servicesManager.CreateLocalRepository().Gems(params)
	}
	return err
}

func localNpmHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewNpmLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Npm(params)
	} else {
		err = servicesManager.CreateLocalRepository().Npm(params)
	}
	return err
}

func localBowerHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewBowerLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Bower(params)
	} else {
		err = servicesManager.CreateLocalRepository().Bower(params)
	}
	return err
}

func localDebianHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewDebianLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Debian(params)
	} else {
		err = servicesManager.CreateLocalRepository().Debian(params)
	}
	return err
}

func localComposerHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewComposerLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Composer(params)
	} else {
		err = servicesManager.CreateLocalRepository().Composer(params)
	}
	return err
}

func localPypiHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewPypiLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Pypi(params)
	} else {
		err = servicesManager.CreateLocalRepository().Pypi(params)
	}
	return err
}

func localDockerHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewDockerLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Docker(params)
	} else {
		err = servicesManager.CreateLocalRepository().Docker(params)
	}
	return err
}

func localVagrantHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewVagrantLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Vagrant(params)
	} else {
		err = servicesManager.CreateLocalRepository().Vagrant(params)
	}
	return err
}

func localGitLfsHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewGitlfsLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Gitlfs(params)
	} else {
		err = servicesManager.CreateLocalRepository().Gitlfs(params)
	}
	return err
}

func localGoHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewGoLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Go(params)
	} else {
		err = servicesManager.CreateLocalRepository().Go(params)
	}
	return err
}

func localYumHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewYumLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Yum(params)
	} else {
		err = servicesManager.CreateLocalRepository().Yum(params)
	}
	return err
}

func localConanHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewConanLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Conan(params)
	} else {
		err = servicesManager.CreateLocalRepository().Conan(params)
	}
	return err
}

func localChefHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewChefLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Chef(params)
	} else {
		err = servicesManager.CreateLocalRepository().Chef(params)
	}
	return err
}

func localPuppetHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewPuppetLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Puppet(params)
	} else {
		err = servicesManager.CreateLocalRepository().Puppet(params)
	}
	return err
}

func localAlpineHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewAlpineLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Alpine(params)
	} else {
		err = servicesManager.CreateLocalRepository().Alpine(params)
	}
	return err
}

func localCondaHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewCondaLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Conda(params)
	} else {
		err = servicesManager.CreateLocalRepository().Conda(params)
	}
	return err
}

func localSwiftHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewSwiftLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}

	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Swift(params)
	} else {
		err = servicesManager.CreateLocalRepository().Swift(params)
	}
	return err
}

func localTerraformHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewTerraformLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}

	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Terraform(params)
	} else {
		err = servicesManager.CreateLocalRepository().Terraform(params)
	}
	return err
}

func localCargoHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewCargoLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}

	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Cargo(params)
	} else {
		err = servicesManager.CreateLocalRepository().Cargo(params)
	}
	return err
}

func localGenericHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewGenericLocalRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}

	if isUpdate {
		err = servicesManager.UpdateLocalRepository().Generic(params)
	} else {
		err = servicesManager.CreateLocalRepository().Generic(params)
	}
	return err
}

var remoteRepoHandlers = map[string]repoHandler{
	Maven:     remoteMavenHandler,
	Gradle:    remoteGradleHandler,
	Ivy:       remoteIvyHandler,
	Sbt:       remoteSbtHandler,
	Helm:      remoteHelmHandler,
	Cocoapods: remoteCocoapodsHandler,
	Opkg:      remoteOpkgHandler,
	Rpm:       remoteRpmHandler,
	Nuget:     remoteNugetHandler,
	Cran:      remoteCranHandler,
	Gems:      remoteGemsHandler,
	Npm:       remoteNpmHandler,
	Bower:     remoteBowerHandler,
	Debian:    remoteDebianHandler,
	Composer:  remoteComposerHandler,
	Pypi:      remotePypiHandler,
	Docker:    remoteDockerHandler,
	Gitlfs:    remoteGitLfsHandler,
	Go:        remoteGoHandler,
	Yum:       remoteYumHandler,
	Conan:     remoteConanHandler,
	Conda:     remoteCondaHandler,
	Chef:      remoteChefHandler,
	Puppet:    remotePuppetHandler,
	P2:        remoteP2Handler,
	Vcs:       remoteVcsHandler,
	Alpine:    remoteAlpineHandler,
	Generic:   remoteGenericHandler,
	Swift:     remoteSwiftHandler,
	Terraform: remoteTerraformHandler,
	Cargo:     remoteCargoHandler,
}

func remoteMavenHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewMavenRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Maven(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Maven(params)
	}
	return err
}

func remoteGradleHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewGradleRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Gradle(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Gradle(params)
	}
	return err
}

func remoteIvyHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewIvyRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Ivy(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Ivy(params)
	}
	return err
}

func remoteSbtHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewSbtRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Sbt(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Sbt(params)
	}
	return err
}

func remoteHelmHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewHelmRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Helm(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Helm(params)
	}
	return err
}

func remoteCocoapodsHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewCocoapodsRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Cocoapods(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Cocoapods(params)
	}
	return err
}

func remoteOpkgHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewOpkgRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Opkg(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Opkg(params)
	}
	return err
}

func remoteRpmHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewRpmRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Rpm(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Rpm(params)
	}
	return err
}

func remoteNugetHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewNugetRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Nuget(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Nuget(params)
	}
	return err
}

func remoteCranHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewCranRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Cran(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Cran(params)
	}
	return err
}

func remoteGemsHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewGemsRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Gems(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Gems(params)
	}
	return err
}

func remoteNpmHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewNpmRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Npm(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Npm(params)
	}
	return err
}

func remoteBowerHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewBowerRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Bower(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Bower(params)
	}
	return err
}

func remoteDebianHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewDebianRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Debian(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Debian(params)
	}
	return err
}

func remoteComposerHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewComposerRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Composer(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Composer(params)
	}
	return err
}

func remotePypiHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewPypiRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Pypi(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Pypi(params)
	}
	return err
}

func remoteDockerHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewDockerRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Docker(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Docker(params)
	}
	return err
}

func remoteGitLfsHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewGitlfsRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Gitlfs(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Gitlfs(params)
	}
	return err
}

func remoteGoHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewGoRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Go(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Go(params)
	}
	return err
}

func remoteConanHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewConanRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Conan(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Conan(params)
	}
	return err
}

func remoteChefHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewChefRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Chef(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Chef(params)
	}
	return err
}

func remotePuppetHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewPuppetRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Puppet(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Puppet(params)
	}
	return err
}

func remoteVcsHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewVcsRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Vcs(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Vcs(params)
	}
	return err
}

func remoteAlpineHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewAlpineRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Alpine(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Alpine(params)
	}
	return err
}

func remoteP2Handler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewP2RemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().P2(params)
	} else {
		err = servicesManager.CreateRemoteRepository().P2(params)
	}
	return err
}

func remoteCondaHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewCondaRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Conda(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Conda(params)
	}
	return err
}

func remoteYumHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewYumRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Yum(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Yum(params)
	}
	return err
}

func remoteSwiftHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewSwiftRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Swift(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Swift(params)
	}
	return err
}

func remoteCargoHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewCargoRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Cargo(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Cargo(params)
	}
	return err
}

func remoteTerraformHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewTerraformRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Terraform(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Terraform(params)
	}
	return err
}

func remoteGenericHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewGenericRemoteRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateRemoteRepository().Generic(params)
	} else {
		err = servicesManager.CreateRemoteRepository().Generic(params)
	}
	return err
}

var federatedRepoHandlers = map[string]repoHandler{
	Maven:     federatedMavenHandler,
	Gradle:    federatedGradleHandler,
	Ivy:       federatedIvyHandles,
	Sbt:       federatedSbtHandler,
	Helm:      federatedHelmHandler,
	Cocoapods: federatedCocoapodsHandler,
	Opkg:      federatedOpkgHandler,
	Rpm:       federatedRpmHandler,
	Nuget:     federatedNugetHandler,
	Cran:      federatedCranHandler,
	Gems:      federatedGemsHandler,
	Npm:       federatedNpmHandler,
	Bower:     federatedBowerHandler,
	Debian:    federatedDebianHandler,
	Composer:  federatedComposerHandler,
	Pypi:      federatedPypiHandler,
	Docker:    federatedDockerHandler,
	Vagrant:   federatedVagrantHandler,
	Gitlfs:    federatedGitLfsHandler,
	Go:        federatedGoHandler,
	Conan:     federatedConanHandler,
	Conda:     federatedCondaHandler,
	Chef:      federatedChefHandler,
	Puppet:    federatedPuppetHandler,
	Alpine:    federatedAlpineHandler,
	Generic:   federatedGenericHandler,
	Yum:       federatedYumHandler,
	Swift:     federatedSwiftHandler,
	Terraform: federatedTerraformHandler,
	Cargo:     federatedCargoHandler,
}

func federatedMavenHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewMavenFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Maven(params)
	}
	return servicesManager.CreateFederatedRepository().Maven(params)
}

func federatedGradleHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewGradleFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Gradle(params)
	}
	return servicesManager.CreateFederatedRepository().Gradle(params)
}

func federatedIvyHandles(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewIvyFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Ivy(params)
	}
	return servicesManager.CreateFederatedRepository().Ivy(params)
}

func federatedSbtHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewSbtFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Sbt(params)
	}
	return servicesManager.CreateFederatedRepository().Sbt(params)
}

func federatedHelmHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewHelmFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Helm(params)
	}
	return servicesManager.CreateFederatedRepository().Helm(params)

}

func federatedCocoapodsHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewCocoapodsFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Cocoapods(params)
	}
	return servicesManager.CreateFederatedRepository().Cocoapods(params)
}

func federatedOpkgHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewOpkgFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Opkg(params)
	}
	return servicesManager.CreateFederatedRepository().Opkg(params)
}

func federatedRpmHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewRpmFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Rpm(params)
	}
	return servicesManager.CreateFederatedRepository().Rpm(params)
}

func federatedNugetHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewNugetFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Nuget(params)
	}
	return servicesManager.CreateFederatedRepository().Nuget(params)
}

func federatedCranHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewCranFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Cran(params)
	}
	return servicesManager.CreateFederatedRepository().Cran(params)
}

func federatedGemsHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewGemsFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Gems(params)
	}
	return servicesManager.CreateFederatedRepository().Gems(params)
}

func federatedNpmHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewNpmFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Npm(params)
	}
	return servicesManager.CreateFederatedRepository().Npm(params)
}

func federatedBowerHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewBowerFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Bower(params)
	}
	return servicesManager.CreateFederatedRepository().Bower(params)
}

func federatedDebianHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewDebianFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Debian(params)
	}
	return servicesManager.CreateFederatedRepository().Debian(params)
}

func federatedComposerHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewComposerFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Composer(params)
	}
	return servicesManager.CreateFederatedRepository().Composer(params)
}

func federatedPypiHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewPypiFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Pypi(params)
	}
	return servicesManager.CreateFederatedRepository().Pypi(params)
}

func federatedDockerHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewDockerFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Docker(params)
	}
	return servicesManager.CreateFederatedRepository().Docker(params)
}

func federatedVagrantHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewVagrantFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Vagrant(params)
	}
	return servicesManager.CreateFederatedRepository().Vagrant(params)
}

func federatedGitLfsHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewGitlfsFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Gitlfs(params)
	}
	return servicesManager.CreateFederatedRepository().Gitlfs(params)
}

func federatedGoHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewGoFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Go(params)
	}
	return servicesManager.CreateFederatedRepository().Go(params)
}

func federatedConanHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewConanFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Conan(params)
	}
	return servicesManager.CreateFederatedRepository().Conan(params)
}

func federatedCondaHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewCondaFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Conda(params)
	}
	return servicesManager.CreateFederatedRepository().Conda(params)
}

func federatedChefHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewChefFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Chef(params)
	}
	return servicesManager.CreateFederatedRepository().Chef(params)
}

func federatedPuppetHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewPuppetFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Puppet(params)
	}
	return servicesManager.CreateFederatedRepository().Puppet(params)
}

func federatedAlpineHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewAlpineFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Alpine(params)
	}
	return servicesManager.CreateFederatedRepository().Alpine(params)
}

func federatedGenericHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewGenericFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}

	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Generic(params)
	}
	return servicesManager.CreateFederatedRepository().Generic(params)
}

func federatedSwiftHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewSwiftFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Swift(params)
	}
	return servicesManager.CreateFederatedRepository().Swift(params)
}

func federatedTerraformHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewTerraformFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Terraform(params)
	}
	return servicesManager.CreateFederatedRepository().Terraform(params)
}

func federatedCargoHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewCargoFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Cargo(params)
	}
	return servicesManager.CreateFederatedRepository().Cargo(params)
}

func federatedYumHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewYumFederatedRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		return servicesManager.UpdateFederatedRepository().Yum(params)
	}
	return servicesManager.CreateFederatedRepository().Yum(params)
}

var virtualRepoHandlers = map[string]repoHandler{
	Maven:     virtualMavenHandler,
	Gradle:    virtualGradleHandler,
	Ivy:       virtualIvyHandler,
	Sbt:       virtualSbtHandler,
	Helm:      virtualHelmHandler,
	Rpm:       virtualRpmHandler,
	Nuget:     virtualNugetHandler,
	Cran:      virtualCranHandler,
	Gems:      virtualGemsHandler,
	Npm:       virtualNpmHandler,
	Bower:     virtualBowerHandler,
	Debian:    virtualDebianHandler,
	Pypi:      virtualPypiHandler,
	Docker:    virtualDockerHandler,
	Gitlfs:    virtualGitLfsHandler,
	Go:        virtualGoHandler,
	Yum:       virtualYumHandler,
	Conan:     virtualConanHandler,
	Chef:      virtualChefHandler,
	Puppet:    virtualPuppetHandler,
	Conda:     virtualCondaHandler,
	P2:        virtualP2Handler,
	Alpine:    virtualAlpineHandler,
	Generic:   virtualGenericHandler,
	Swift:     virtualSwiftHandler,
	Terraform: virtualTerraformHandler,
}

func virtualMavenHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewMavenVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Maven(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Maven(params)
	}
	return err
}

func virtualGradleHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewGradleVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Gradle(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Gradle(params)
	}
	return err
}

func virtualIvyHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewIvyVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Ivy(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Ivy(params)
	}
	return err
}

func virtualSbtHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewSbtVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Sbt(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Sbt(params)
	}
	return err
}

func virtualHelmHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewHelmVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Helm(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Helm(params)
	}
	return err
}

func virtualRpmHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewRpmVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Rpm(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Rpm(params)
	}
	return err
}

func virtualNugetHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewNugetVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Nuget(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Nuget(params)
	}
	return err
}

func virtualCranHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewCranVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Cran(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Cran(params)
	}
	return err
}

func virtualGemsHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewGemsVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Gems(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Gems(params)
	}
	return err
}

func virtualNpmHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewNpmVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Npm(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Npm(params)
	}
	return err
}

func virtualBowerHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewBowerVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Bower(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Bower(params)
	}
	return err
}

func virtualDebianHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewDebianVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Debian(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Debian(params)
	}
	return err
}

func virtualPypiHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewPypiVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Pypi(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Pypi(params)
	}
	return err
}

func virtualDockerHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewDockerVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Docker(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Docker(params)
	}
	return err
}

func virtualGitLfsHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewGitlfsVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Gitlfs(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Gitlfs(params)
	}
	return err
}

func virtualGoHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewGoVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Go(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Go(params)
	}
	return err
}

func virtualConanHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewConanVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Conan(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Conan(params)
	}
	return err
}

func virtualChefHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewChefVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Chef(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Chef(params)
	}
	return err
}

func virtualPuppetHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewPuppetVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Puppet(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Puppet(params)
	}
	return err
}

func virtualYumHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewYumVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Yum(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Yum(params)
	}
	return err
}

func virtualP2Handler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewP2VirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().P2(params)
	} else {
		err = servicesManager.CreateVirtualRepository().P2(params)
	}
	return err
}

func virtualAlpineHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewAlpineVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Alpine(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Alpine(params)
	}
	return err
}

func virtualCondaHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewCondaVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Conda(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Conda(params)
	}
	return err
}

func virtualSwiftHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewSwiftVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Swift(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Swift(params)
	}
	return err
}

func virtualTerraformHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewTerraformVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Terraform(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Terraform(params)
	}
	return err
}

func virtualGenericHandler(servicesManager artifactory.ArtifactoryServicesManager, jsonConfig []byte, isUpdate bool) error {
	params := services.NewGenericVirtualRepositoryParams()
	err := json.Unmarshal(jsonConfig, &params)
	if errorutils.CheckError(err) != nil {
		return err
	}
	if isUpdate {
		err = servicesManager.UpdateVirtualRepository().Generic(params)
	} else {
		err = servicesManager.CreateVirtualRepository().Generic(params)
	}
	return err
}
