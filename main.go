//go:generate go run $GOPATH/src/github.com/mjibson/esc/main.go -o ./resources/RESOURCES.go -pkg resources ./resources/source

package main

import (
	"encoding/json"
	"fmt"
	"github.com/Sirupsen/logrus"
	"github.com/asaskevich/govalidator"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	gocf "github.com/crewjam/go-cloudformation"
	sparta "github.com/mweagle/Sparta"
	spartaAWS "github.com/mweagle/Sparta/aws"
	spartaCF "github.com/mweagle/Sparta/aws/cloudformation"
	spartaIAM "github.com/mweagle/Sparta/aws/iam"

	"./concourse"
	"./resources"
	"github.com/spf13/cobra"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	databaseMasterUsername = "concourse"
	// The parameter MasterUserPassword is not a valid password because it is shorter than 8 characters.
	databaseMasterPassword = "picard1337"
)

// Additional command line options used for both the provision
// and CLI commands
type optionsStruct struct {
	Username   string `valid:"required,match(\\w+)"`
	Password   string `valid:"required,match(\\w+)"`
	SSHKeyName string `valid:"-"`
}

var options optionsStruct

// Nothing super interesting to do here.  In a real system you might
// actually define the lambda functions in the same codebase.
func ciCDConfigurator(event *json.RawMessage,
	context *sparta.LambdaContext,
	w http.ResponseWriter,
	logger *logrus.Logger) {

	configuration, _ := sparta.Discover()

	logger.WithFields(logrus.Fields{
		"Discovery": configuration,
	}).Info("Custom resource request")

	fmt.Fprint(w, "Hello World")
}

// Given a slice of EC2 images, return the most recently Created one.
func mostRecentImageID(images []*ec2.Image) (string, error) {
	if len(images) <= 0 {
		return "", fmt.Errorf("No images to search")
	}
	var mostRecentImage *ec2.Image
	for _, eachImage := range images {
		if nil == mostRecentImage {
			mostRecentImage = eachImage
		} else {
			curTime, curTimeErr := time.Parse(time.RFC3339, *mostRecentImage.CreationDate)
			if nil != curTimeErr {
				return "", curTimeErr
			}
			testTime, testTimeErr := time.Parse(time.RFC3339, *eachImage.CreationDate)
			if nil != testTimeErr {
				return "", testTimeErr
			}
			if (testTime.Unix() - curTime.Unix()) > 0 {
				mostRecentImage = eachImage
			}
		}
	}
	return *mostRecentImage.ImageId, nil
}

// Lambda CustomResource function that looks up the latest Ubuntu AMI ID for the
// current region and returns a map with the latest AMI IDs via the resource's
// outputs.  For this example we're going to look for the latest Ubuntu 16.04
// release.
// Ref: https://help.ubuntu.com/community/EC2StartersGuide#Official_Ubuntu_Cloud_Guest_Amazon_Machine_Images_.28AMIs.29
func ubuntuAMICustomResource(requestType string,
	stackID string,
	properties map[string]interface{},
	logger *logrus.Logger) (map[string]interface{}, error) {
	if requestType != "Create" {
		return map[string]interface{}{}, nil
	}

	// Setup the common filters
	describeImageFilters := []*ec2.Filter{}
	describeImageFilters = append(describeImageFilters, &ec2.Filter{
		Name:   aws.String("name"),
		Values: []*string{aws.String("*hvm-ssd/ubuntu-xenial-16.04-amd64-server*")},
	})
	describeImageFilters = append(describeImageFilters, &ec2.Filter{
		Name:   aws.String("root-device-type"),
		Values: []*string{aws.String("ebs")},
	})
	describeImageFilters = append(describeImageFilters, &ec2.Filter{
		Name:   aws.String("architecture"),
		Values: []*string{aws.String("x86_64")},
	})
	describeImageFilters = append(describeImageFilters, &ec2.Filter{
		Name:   aws.String("virtualization-type"),
		Values: []*string{aws.String("hvm")},
	})

	// Get the HVM AMIs
	params := &ec2.DescribeImagesInput{
		Filters: describeImageFilters,
		Owners:  []*string{aws.String("099720109477")},
	}
	logger.Level = logrus.DebugLevel
	ec2Svc := ec2.New(spartaAWS.NewSession(logger))
	describeImagesOutput, describeImagesOutputErr := ec2Svc.DescribeImages(params)
	if nil != describeImagesOutputErr {
		return nil, describeImagesOutputErr
	}
	logger.WithFields(logrus.Fields{
		"DescribeImagesOutput":    describeImagesOutput,
		"DescribeImagesOutputErr": describeImagesOutputErr,
	}).Info("Results")

	amiID, amiIDErr := mostRecentImageID(describeImagesOutput.Images)
	if nil != amiIDErr {
		return nil, amiIDErr
	}

	// Set the HVM type
	outputProps := map[string]interface{}{
		"HVM": amiID,
	}
	logger.WithFields(logrus.Fields{
		"Outputs": outputProps,
	}).Info("CustomResource outputs")
	return outputProps, nil
}

// The CloudFormation template decorator that inserts all the other
// AWS components we need to support this deployment...
func ciCDLambdaDecorator(customResourceAMILookupName string,
	ec2SecurityGroupResourceName string) sparta.TemplateDecorator {

	return func(serviceName string,
		lambdaResourceName string,
		lambdaResource gocf.LambdaFunction,
		resourceMetadata map[string]interface{},
		S3Bucket string,
		S3Key string,
		template *gocf.Template,
		logger *logrus.Logger) error {
		// Create the launch configuration with Metadata to download the ZIP file, unzip it & launch the
		// golang binary...
		dbSecurityGroupName := sparta.CloudFormationResourceName("ConcoursePostgresqlSG",
			"ConcoursePostgresqlSG")
		dbInstanceName := sparta.CloudFormationResourceName("ConcoursePostgresql",
			"ConcoursePostgresql")
		asgLaunchConfigurationName := sparta.CloudFormationResourceName("ConcourseCIASGLaunchConfig",
			"ConcourseCIASGLaunchConfig")
		asgResourceName := sparta.CloudFormationResourceName("ConcourseCIASG",
			"ConcourseCIASG")
		ec2InstanceRoleName := sparta.CloudFormationResourceName("ConcourseCIEC2InstanceRole",
			"ConcourseCIEC2InstanceRole")
		ec2InstanceProfileName := sparta.CloudFormationResourceName("ConcourseCIEC2InstanceProfile",
			"ConcourseCIEC2InstanceProfile")

		//////////////////////////////////////////////////////////////////////////////
		// 1 - Create the security group for the Concourse EC2 instance
		ec2SecurityGroup := &gocf.EC2SecurityGroup{
			GroupDescription: gocf.String("Concourse CI/CD Group"),
			SecurityGroupIngress: &gocf.EC2SecurityGroupRuleList{
				gocf.EC2SecurityGroupRule{
					CidrIp:     gocf.String("0.0.0.0/0"),
					IpProtocol: gocf.String("tcp"),
					FromPort:   gocf.Integer(8080),
					ToPort:     gocf.Integer(8080),
				},
				gocf.EC2SecurityGroupRule{
					CidrIp:     gocf.String("0.0.0.0/0"),
					IpProtocol: gocf.String("tcp"),
					FromPort:   gocf.Integer(22),
					ToPort:     gocf.Integer(22),
				},
			},
		}
		// TODO - when deploying to a VPC, remove this when
		// https://forums.aws.amazon.com/thread.jspa?messageID=713978
		// is fixed
		/*ec2SecurityGroupResource := */ template.AddResource(ec2SecurityGroupResourceName, ec2SecurityGroup)
		//ec2SecurityGroupResource.DeletionPolicy = "Retain"

		//////////////////////////////////////////////////////////////////////////////
		// 2 - Create the DB Instance, enable access from the EC2 instance...
		dbSecurityGroup := &gocf.RDSDBSecurityGroup{
			GroupDescription: gocf.String("Allow access from Concourse CI server"),
			DBSecurityGroupIngress: &gocf.RDSSecurityGroupRuleList{
				gocf.RDSSecurityGroupRule{
					EC2SecurityGroupName:    gocf.Ref(ec2SecurityGroupResourceName).String(),
					EC2SecurityGroupOwnerId: gocf.Ref("AWS::AccountId").String(),
				},
			},
		}
		template.AddResource(dbSecurityGroupName, dbSecurityGroup)

		// Create the DB instance
		dbInstance := &gocf.RDSDBInstance{
			DBName:             gocf.String("atc"),
			Engine:             gocf.String("postgres"),
			AllocatedStorage:   gocf.String("10"),
			DBInstanceClass:    gocf.String("db.t2.micro"),
			MasterUsername:     gocf.String(databaseMasterUsername),
			MasterUserPassword: gocf.String(databaseMasterPassword),
			DBSecurityGroups: gocf.StringList(
				gocf.Ref(dbSecurityGroupName),
			),
		}
		dbCFResource := template.AddResource(dbInstanceName, dbInstance)
		dbCFResource.DependsOn = append(dbCFResource.DependsOn, ec2SecurityGroupResourceName)

		//////////////////////////////////////////////////////////////////////////////
		// 3 - Create the ASG and associate the userdata with the EC2 init
		// EC2 Instance Role...
		statements := sparta.CommonIAMStatements.Core
		statements = append(statements, spartaIAM.PolicyStatement{
			Effect:   "Allow",
			Action:   []string{"*"},
			Resource: gocf.String("*"),
		})

		statements = append(statements, spartaIAM.PolicyStatement{
			Effect:   "Allow",
			Action:   []string{"s3:GetObject"},
			Resource: gocf.String(fmt.Sprintf("arn:aws:s3:::%s/%s", S3Bucket, S3Key)),
		})
		// Enable all access to the S3 bucket s.t. the Version SemVer resource
		// can manipulate objects.  This could be more tightly scoped.
		statements = append(statements, spartaIAM.PolicyStatement{
			Effect:   "Allow",
			Action:   []string{"s3:*"},
			Resource: gocf.String(fmt.Sprintf("arn:aws:s3:::%s", S3Bucket)),
		})

		iamPolicyList := gocf.IAMPoliciesList{}
		iamPolicyList = append(iamPolicyList,
			gocf.IAMPolicies{
				PolicyDocument: sparta.ArbitraryJSONObject{
					"Version":   "2012-10-17",
					"Statement": statements,
				},
				PolicyName: gocf.String("EC2Policy"),
			},
		)
		ec2InstanceRole := &gocf.IAMRole{
			AssumeRolePolicyDocument: sparta.AssumePolicyDocument,
			Policies:                 &iamPolicyList,
		}
		template.AddResource(ec2InstanceRoleName, ec2InstanceRole)

		// Create the instance profile
		ec2InstanceProfile := &gocf.IAMInstanceProfile{
			Path:  gocf.String("/"),
			Roles: []gocf.Stringable{gocf.Ref(ec2InstanceRoleName).String()},
		}
		template.AddResource(ec2InstanceProfileName, ec2InstanceProfile)

		//Now setup the properties map, expand the userdata, and attach it...
		userDataProps := map[string]interface{}{
			"S3Bucket":               S3Bucket,
			"S3Key":                  S3Key,
			"ServiceName":            serviceName,
			"DBInstanceResourceName": dbInstanceName,
			"DBInstanceUser":         databaseMasterUsername,
			"DBInstancePassword":     databaseMasterPassword,
			"DBInstanceDatabaseName": "atc",
			"Username":               options.Username,
			"Password":               options.Password,
		}

		userDataTemplateInput, userDataTemplateInputErr := resources.FSString(false, "/resources/source/userdata.sh")
		if nil != userDataTemplateInputErr {
			return userDataTemplateInputErr
		}
		userDataExpression, userDataExpressionErr := spartaCF.ConvertToTemplateExpression(strings.NewReader(userDataTemplateInput), userDataProps)
		if nil != userDataExpressionErr {
			return userDataExpressionErr
		}

		logger.WithFields(logrus.Fields{
			"Parameters": userDataProps,
			"Expanded":   userDataExpression,
		}).Debug("Expanded userdata")

		asgLaunchConfigurationResource := &gocf.AutoScalingLaunchConfiguration{
			ImageId:            gocf.GetAtt(customResourceAMILookupName, "HVM"),
			InstanceType:       gocf.String("t2.micro"),
			KeyName:            gocf.String(options.SSHKeyName),
			IamInstanceProfile: gocf.Ref(ec2InstanceProfileName).String(),
			UserData:           gocf.Base64(userDataExpression),
			SecurityGroups:     gocf.StringList(gocf.GetAtt(ec2SecurityGroupResourceName, "GroupId")),
		}
		launchConfigResource := template.AddResource(asgLaunchConfigurationName,
			asgLaunchConfigurationResource)
		launchConfigResource.DependsOn = append(launchConfigResource.DependsOn,
			customResourceAMILookupName)

		// Create the ASG
		asgResource := &gocf.AutoScalingAutoScalingGroup{
			// Empty Region is equivalent to all region AZs
			// Ref: http://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/intrinsic-function-reference-getavailabilityzones.html
			AvailabilityZones:       gocf.GetAZs(gocf.String("")),
			LaunchConfigurationName: gocf.Ref(asgLaunchConfigurationName).String(),
			MaxSize:                 gocf.String("1"),
			MinSize:                 gocf.String("1"),
		}
		template.AddResource(asgResourceName, asgResource)
		return nil
	}
}

func registerSpartaCICDFlags(command *cobra.Command) {
	command.Flags().StringVarP(&options.Username,
		"username",
		"u",
		"",
		"Concourse HTTP Basic Auth username")
	command.Flags().StringVarP(&options.Password,
		"password",
		"p",
		"",
		"Concourse HTTP Basic Auth password")

}

////////////////////////////////////////////////////////////////////////////////
// Main
func main() {

	// Custom command to handle rebuilding pipelines
	// Add the custom command to run the sync loop
	syncCommand := &cobra.Command{
		Use:   "sync",
		Short: "Periodically rebuild Concourse pipelines",
		Long:  `Periodically scan the repo for pipelines and update them with ec2metadata credentials`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return concourse.SyncIt(options.Username, options.Password, sparta.OptionsGlobal.Logger)
		},
	}
	// Include the basic auth flags for the sync command
	registerSpartaCICDFlags(syncCommand)
	sparta.CommandLineOptions.Root.AddCommand(syncCommand)

	// Add them to the standard provision command
	registerSpartaCICDFlags(sparta.CommandLineOptions.Provision)

	// And add the SSHKeyName option
	sparta.CommandLineOptions.Provision.Flags().StringVarP(&options.SSHKeyName,
		"key",
		"k",
		"",
		"SSH Key Name to use for EC2 instances")

	// Define a validation hook s.t. we can verify the subnetIDs command line
	// option is acceptable...
	validationHook := func(command *cobra.Command) error {
		if command.Name() == "provision" && len(options.SSHKeyName) <= 0 {
			return fmt.Errorf("SSHKeyName option is required")
		}
		fmt.Printf("Command: %s\n", command.Name())
		switch command.Name() {
		case "provision",
			"sync":
			_, validationErr := govalidator.ValidateStruct(options)
			return validationErr
		default:
			return nil
		}
	}
	// What are the subnets?
	parseErr := sparta.ParseOptions(validationHook)
	if nil != parseErr {
		os.Exit(3)
	}
	ec2SecurityGroupName := sparta.CloudFormationResourceName("ConcourseSecurityGroup",
		"ConcourseSecurityGroup")

	// The primary lambda function
	lambdaFn := sparta.NewLambda(sparta.IAMRoleDefinition{},
		ciCDConfigurator,
		nil)

	// Lambda custom resource to lookup the latest Ubuntu AMIs
	iamRoleCustomResource := sparta.IAMRoleDefinition{}
	iamRoleCustomResource.Privileges = append(iamRoleCustomResource.Privileges,
		sparta.IAMRolePrivilege{
			Actions:  []string{"ec2:DescribeImages"},
			Resource: "*",
		})

	customResourceLambdaOptions := sparta.LambdaFunctionOptions{
		MemorySize: 128,
		Timeout:    30,
	}
	amiIDCustomResourceName, _ := lambdaFn.RequireCustomResource(iamRoleCustomResource,
		ubuntuAMICustomResource,
		&customResourceLambdaOptions,
		nil)

	// Get the resource name and pass it to the decorator
	lambdaFn.Decorator = ciCDLambdaDecorator(amiIDCustomResourceName,
		ec2SecurityGroupName)
	var lambdaFunctions []*sparta.LambdaAWSInfo
	lambdaFunctions = append(lambdaFunctions, lambdaFn)

	err := sparta.Main("SpartaCICD",
		fmt.Sprintf("Provision a Concourse CICD system"),
		lambdaFunctions,
		nil,
		nil)
	if err != nil {
		os.Exit(1)
	}
}
