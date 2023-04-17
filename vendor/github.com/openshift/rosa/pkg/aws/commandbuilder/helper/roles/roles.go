package roles

import (
	"fmt"

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	awscb "github.com/openshift/rosa/pkg/aws/commandbuilder"
	"github.com/openshift/rosa/pkg/aws/tags"
)

type ManualCommandsForMissingOperatorRolesInput struct {
	ClusterID                string
	OperatorRolePolicyPrefix string
	Operator                 *cmv1.STSOperator
	RoleName                 string
	Filename                 string
	RolePath                 string
	PolicyARN                string
	ManagedPolicies          bool
}

func ManualCommandsForMissingOperatorRole(input ManualCommandsForMissingOperatorRolesInput) []string {
	commands := make([]string, 0)
	iamTags := map[string]string{
		tags.ClusterID:         input.ClusterID,
		tags.RolePrefix:        input.OperatorRolePolicyPrefix,
		tags.OperatorNamespace: input.Operator.Namespace(),
		tags.OperatorName:      input.Operator.Name(),
		tags.RedHatManaged:     "true",
	}
	if input.ManagedPolicies {
		iamTags[tags.ManagedPolicies] = "true"
	}

	createRole := awscb.NewIAMCommandBuilder().
		SetCommand(awscb.CreateRole).
		AddParam(awscb.RoleName, input.RoleName).
		AddParam(awscb.AssumeRolePolicyDocument, fmt.Sprintf("file://%s", input.Filename)).
		AddTags(iamTags).
		AddParam(awscb.Path, input.RolePath).
		Build()
	attachRolePolicy := awscb.NewIAMCommandBuilder().
		SetCommand(awscb.AttachRolePolicy).
		AddParam(awscb.RoleName, input.RoleName).
		AddParam(awscb.PolicyArn, input.PolicyARN).
		Build()
	commands = append(commands, createRole, attachRolePolicy)
	return commands
}

type ManualCommandsForUpgradeOperatorRolePolicyInput struct {
	HasPolicy                                bool
	OperatorRolePolicyPrefix                 string
	Operator                                 *cmv1.STSOperator
	CredRequest                              string
	OperatorPolicyPath                       string
	PolicyARN                                string
	DefaultPolicyVersion                     string
	PolicyName                               string
	HasDetachPolicyCommandsForExpectedPolicy bool
	OperatorRoleName                         string
}

func ManualCommandsForUpgradeOperatorRolePolicy(input ManualCommandsForUpgradeOperatorRolePolicyInput) []string {
	commands := make([]string, 0)
	if !input.HasPolicy {
		iamTags := map[string]string{
			tags.OpenShiftVersion:  input.DefaultPolicyVersion,
			tags.RolePrefix:        input.OperatorRolePolicyPrefix,
			tags.OperatorNamespace: input.Operator.Namespace(),
			tags.OperatorName:      input.Operator.Name(),
			tags.RedHatManaged:     "true",
		}
		createPolicy := awscb.NewIAMCommandBuilder().
			SetCommand(awscb.CreatePolicy).
			AddParam(awscb.PolicyName, input.PolicyName).
			AddParam(awscb.PolicyDocument, fmt.Sprintf("file://openshift_%s_policy.json", input.CredRequest)).
			AddTags(iamTags).
			AddParam(awscb.Path, input.OperatorPolicyPath).
			Build()
		commands = append(commands, createPolicy)
	} else {
		if input.HasDetachPolicyCommandsForExpectedPolicy {
			attachRolePolicy := awscb.NewIAMCommandBuilder().
				SetCommand(awscb.AttachRolePolicy).
				AddParam(awscb.RoleName, input.OperatorRoleName).
				AddParam(awscb.PolicyArn, input.PolicyARN).
				Build()
			commands = append(commands, attachRolePolicy)
		}
		policyTags := map[string]string{
			tags.OpenShiftVersion: input.DefaultPolicyVersion,
		}

		createPolicyVersion := awscb.NewIAMCommandBuilder().
			SetCommand(awscb.CreatePolicyVersion).
			AddParam(awscb.PolicyArn, input.PolicyARN).
			AddParam(awscb.PolicyDocument, fmt.Sprintf("file://openshift_%s_policy.json", input.CredRequest)).
			AddParamNoValue(awscb.SetAsDefault).
			Build()

		tagPolicy := awscb.NewIAMCommandBuilder().
			SetCommand(awscb.TagPolicy).
			AddTags(policyTags).
			AddParam(awscb.PolicyArn, input.PolicyARN).
			Build()
		commands = append(commands, createPolicyVersion, tagPolicy)
	}
	return commands
}

type ManualCommandsForUpgradeAccountRolePolicyInput struct {
	DefaultPolicyVersion                     string
	RoleName                                 string
	HasPolicy                                bool
	Prefix                                   string
	File                                     string
	PolicyName                               string
	AccountPolicyPath                        string
	PolicyARN                                string
	HasInlinePolicy                          bool
	HasDetachPolicyCommandsForExpectedPolicy bool
}

func ManualCommandsForUpgradeAccountRolePolicy(input ManualCommandsForUpgradeAccountRolePolicyInput) []string {
	commands := make([]string, 0)
	iamRoleTags := map[string]string{
		tags.OpenShiftVersion: input.DefaultPolicyVersion,
	}

	tagRole := awscb.NewIAMCommandBuilder().
		SetCommand(awscb.TagRole).
		AddTags(iamRoleTags).
		AddParam(awscb.RoleName, input.RoleName).
		Build()

	attachRolePolicy := awscb.NewIAMCommandBuilder().
		SetCommand(awscb.AttachRolePolicy).
		AddParam(awscb.RoleName, input.RoleName).
		AddParam(awscb.PolicyArn, input.PolicyARN).
		Build()
	if !input.HasPolicy {
		iamTags := map[string]string{
			tags.OpenShiftVersion: input.DefaultPolicyVersion,
			tags.RolePrefix:       input.Prefix,
			tags.RoleType:         input.File,
			tags.RedHatManaged:    "true",
		}
		createPolicy := awscb.NewIAMCommandBuilder().
			SetCommand(awscb.CreatePolicy).
			AddParam(awscb.PolicyName, input.PolicyName).
			AddParam(awscb.PolicyDocument, fmt.Sprintf("file://sts_%s_permission_policy.json", input.File)).
			AddTags(iamTags).
			AddParam(awscb.Path, input.AccountPolicyPath).
			Build()

		if input.HasInlinePolicy {
			deletePolicy := awscb.NewIAMCommandBuilder().
				SetCommand(awscb.DeleteRolePolicy).
				AddParam(awscb.RoleName, input.RoleName).
				AddParam(awscb.PolicyName, input.PolicyName).
				Build()
			commands = append(commands, deletePolicy)
		}
		commands = append(commands, createPolicy, attachRolePolicy, tagRole)
	} else {
		if input.HasDetachPolicyCommandsForExpectedPolicy {
			commands = append(commands, attachRolePolicy)
		}
		createPolicyVersion := awscb.NewIAMCommandBuilder().
			SetCommand(awscb.CreatePolicyVersion).
			AddParam(awscb.PolicyArn, input.PolicyARN).
			AddParam(awscb.PolicyDocument, fmt.Sprintf("file://sts_%s_permission_policy.json", input.File)).
			AddParamNoValue(awscb.SetAsDefault).
			Build()

		tagPolicies := awscb.NewIAMCommandBuilder().
			SetCommand(awscb.TagPolicy).
			AddTags(iamRoleTags).
			AddParam(awscb.PolicyArn, input.PolicyARN).
			Build()
		commands = append(commands, createPolicyVersion, tagPolicies, tagRole)
	}
	return commands
}

type ManualCommandsForDetachRolePolicyInput struct {
	RoleName  string
	PolicyARN string
}

func ManualCommandsForDetachRolePolicy(input ManualCommandsForDetachRolePolicyInput) string {
	return awscb.NewIAMCommandBuilder().
		SetCommand(awscb.DetachRolePolicy).
		AddParam(awscb.RoleName, input.RoleName).
		AddParam(awscb.PolicyArn, input.PolicyARN).
		Build()
}
