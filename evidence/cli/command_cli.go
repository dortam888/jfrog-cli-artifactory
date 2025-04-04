package cli

import (
	"errors"
	"github.com/jfrog/jfrog-cli-artifactory/evidence/cli/docs/create"
	commonCliUtils "github.com/jfrog/jfrog-cli-core/v2/common/cliutils"
	"github.com/jfrog/jfrog-cli-core/v2/common/commands"
	pluginsCommon "github.com/jfrog/jfrog-cli-core/v2/plugins/common"
	"github.com/jfrog/jfrog-cli-core/v2/plugins/components"
	coreConfig "github.com/jfrog/jfrog-cli-core/v2/utils/config"
	coreUtils "github.com/jfrog/jfrog-cli-core/v2/utils/coreutils"
	"github.com/jfrog/jfrog-client-go/utils"
	"github.com/jfrog/jfrog-client-go/utils/errorutils"
	"os"
	"strings"
)

func GetCommands() []components.Command {
	return []components.Command{
		{
			Name:        "create-evidence",
			Aliases:     []string{"create"},
			Flags:       GetCommandFlags(CreateEvidence),
			Description: create.GetDescription(),
			Arguments:   create.GetArguments(),
			Action:      createEvidence,
		},
	}
}

var execFunc = commands.Exec

func createEvidence(ctx *components.Context) error {
	if err := validateCreateEvidenceCommonContext(ctx); err != nil {
		return err
	}
	subject, err := getAndValidateSubject(ctx)
	if err != nil {
		return err
	}
	serverDetails, err := evidenceDetailsByFlags(ctx)
	if err != nil {
		return err
	}

	var command EvidenceCommands
	switch subject {
	case subjectRepoPath:
		command = NewEvidenceCustomCommand(ctx, execFunc)
	case releaseBundle:
		command = NewEvidenceReleaseBundleCommand(ctx, execFunc)
	case buildName:
		command = NewEvidenceBuildCommand(ctx, execFunc)
	case packageName:
		command = NewEvidencePackageCommand(ctx, execFunc)
	default:
		return errors.New("unsupported subject")
	}

	return command.CreateEvidence(ctx, serverDetails)
}

func validateCreateEvidenceCommonContext(ctx *components.Context) error {
	if show, err := pluginsCommon.ShowCmdHelpIfNeeded(ctx, ctx.Arguments); show || err != nil {
		return err
	}

	if len(ctx.Arguments) > 1 {
		return pluginsCommon.WrongNumberOfArgumentsHandler(ctx)
	}

	if !ctx.IsFlagSet(predicate) || assertValueProvided(ctx, predicate) != nil {
		return errorutils.CheckErrorf("'predicate' is a mandatory field for creating evidence: --%s", predicate)
	}

	if !ctx.IsFlagSet(predicateType) || assertValueProvided(ctx, predicateType) != nil {
		return errorutils.CheckErrorf("'predicate-type' is a mandatory field for creating evidence: --%s", predicateType)
	}

	if err := ensureKeyExists(ctx, key); err != nil {
		return err
	}

	if !ctx.IsFlagSet(keyAlias) {
		setKeyAliasIfProvided(ctx, keyAlias)
	}

	return nil
}

func ensureKeyExists(ctx *components.Context, key string) error {
	if ctx.IsFlagSet(key) && assertValueProvided(ctx, key) == nil {
		return nil
	}

	signingKeyValue, _ := getEnvVariable(coreUtils.SigningKey)
	if signingKeyValue == "" {
		return errorutils.CheckErrorf("JFROG_CLI_SIGNING_KEY env variable or --%s flag must be provided when creating evidence", key)
	}
	ctx.AddStringFlag(key, signingKeyValue)
	return nil
}

func setKeyAliasIfProvided(ctx *components.Context, keyAlias string) {
	evdKeyAliasValue, _ := getEnvVariable(coreUtils.KeyAlias)
	if evdKeyAliasValue != "" {
		ctx.AddStringFlag(keyAlias, evdKeyAliasValue)
	}
}

func getAndValidateSubject(ctx *components.Context) (string, error) {
	var foundSubjects []string
	for _, key := range subjectTypes {
		if ctx.GetStringFlagValue(key) != "" {
			foundSubjects = append(foundSubjects, key)
		}
	}

	if len(foundSubjects) == 0 {
		// If we have no subject - we will try to create EVD on build
		if !attemptSetBuildNameAndNumber(ctx) {
			return "", errorutils.CheckErrorf("subject must be one of the fields: [%s]", strings.Join(subjectTypes, ", "))
		}
		foundSubjects = append(foundSubjects, buildName)
	}

	if err := validateFoundSubjects(foundSubjects); err != nil {
		return "", err
	}

	return foundSubjects[0], nil
}

func attemptSetBuildNameAndNumber(ctx *components.Context) bool {
	buildNameAdded := setBuildValue(ctx, buildName, coreUtils.BuildName)
	buildNumberAdded := setBuildValue(ctx, buildNumber, coreUtils.BuildNumber)

	return buildNameAdded && buildNumberAdded
}

func setBuildValue(ctx *components.Context, flag, envVar string) bool {
	// Check if the flag is provided. If so, we use it.
	if ctx.IsFlagSet(flag) {
		return true
	}
	// If the flag is not set, then check the environment variable
	if currentValue := os.Getenv(envVar); currentValue != "" {
		ctx.AddStringFlag(flag, currentValue)
		return true
	}
	return false
}

func validateFoundSubjects(foundSubjects []string) error {
	if len(foundSubjects) > 1 {
		return errorutils.CheckErrorf("multiple subjects found: [%s]", strings.Join(foundSubjects, ", "))
	}
	return nil
}

func evidenceDetailsByFlags(ctx *components.Context) (*coreConfig.ServerDetails, error) {
	serverDetails, err := pluginsCommon.CreateServerDetailsWithConfigOffer(ctx, true, commonCliUtils.Platform)
	if err != nil {
		return nil, err
	}
	if serverDetails.Url == "" {
		return nil, errors.New("platform URL is mandatory for evidence commands")
	}
	platformToEvidenceUrls(serverDetails)

	if serverDetails.GetUser() != "" && serverDetails.GetPassword() != "" {
		return nil, errors.New("evidence service does not support basic authentication")
	}

	return serverDetails, nil
}

func platformToEvidenceUrls(rtDetails *coreConfig.ServerDetails) {
	rtDetails.ArtifactoryUrl = utils.AddTrailingSlashIfNeeded(rtDetails.Url) + "artifactory/"
	rtDetails.EvidenceUrl = utils.AddTrailingSlashIfNeeded(rtDetails.Url) + "evidence/"
	rtDetails.MetadataUrl = utils.AddTrailingSlashIfNeeded(rtDetails.Url) + "metadata/"
}

func assertValueProvided(c *components.Context, fieldName string) error {
	if c.GetStringFlagValue(fieldName) == "" {
		return errorutils.CheckErrorf("the --%s option is mandatory", fieldName)
	}
	return nil
}
