// Copyright (c) 2021 Terminus, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package apipolicy

import (
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"

	mseCommon "github.com/erda-project/erda/internal/tools/orchestrator/hepa/gateway-providers/mse/common"
	"github.com/erda-project/erda/internal/tools/orchestrator/hepa/repository/orm"
	db "github.com/erda-project/erda/internal/tools/orchestrator/hepa/repository/service"
)

type IngressAnnotation struct {
	Annotation      map[string]*string
	LocationSnippet *string
}

type IngressController struct {
	ConfigOption  map[string]*string
	MainSnippet   *string
	HttpSnippet   *string
	ServerSnippet *string
}

type PolicyConfig struct {
	KongPolicyChange  bool
	IngressAnnotation *IngressAnnotation
	IngressController *IngressController
	AnnotationReset   bool
}

type ServiceInfo struct {
	ProjectName string
	Env         string
}

const (
	CTX_IDENTIFY     = "id"
	CTX_K8S_CLIENT   = "k8s_client"
	CTX_KONG_ADAPTER = "kong_adapter"
	CTX_MSE_ADAPTER  = "mse_adapter"
	CTX_ZONE         = "zone"
	CTX_SERVICE_INFO = "service_info"

	Policy_Engine_Service_Guard = "safety-server-guard"
	Policy_Engine_Built_in      = "built-in"
	Policy_Engine_WAF           = "safety-waf"
	Policy_Engine_CORS          = "cors"
	Policy_Engine_Custom        = "custom"
	Policy_Engine_IP            = "safety-ip"
	Policy_Engine_Proxy         = "proxy"
	Policy_Engine_SBAC          = "sbac" // "sbac" is ServerBasedAccessControl
	Policy_Engine_CSRF          = "safety-csrf"

	Policy_Category_Basic   = "basic"
	Policy_Category_BuiltIn = "built-in"
	Policy_Category_Safety  = "safety"
	Policy_Category_Proxy   = "proxy"
	Policy_Category_Auth    = "auth"
)

type PolicyEngine interface {
	GetConfig(string, string, *orm.GatewayZone, map[string]interface{}) (PolicyDto, error)
	MergeDiceConfig(map[string]interface{}) (PolicyDto, error)
	CreateDefaultConfig(map[string]interface{}) PolicyDto
	ParseConfig(PolicyDto, map[string]interface{}, bool) (PolicyConfig, error)
	NeedResetAnnotation(PolicyDto) bool
	UnmarshalConfig([]byte, string) (PolicyDto, error, string)
	SetName(name string)
	GetName() string
	NeedSerialUpdate() bool
}

var registerMap = map[string]PolicyEngine{}

func GetPolicyEngine(name string) (PolicyEngine, error) {
	engine, exist := registerMap[name]
	if !exist {
		return nil, errors.Errorf("policy engine not find, name:%s", name)
	}
	return engine, nil
}

func RegisterPolicyEngine(name string, engine PolicyEngine) error {
	_, exist := registerMap[name]
	if exist {
		return errors.Errorf("policy engine already registered, name:%s", name)
	}
	registerMap[name] = engine
	engine.SetName(name)
	log.Infof("policy engine registerd, name:%s", name)
	return nil
}

type PolicyDto interface {
	SetGlobal(bool)
	IsGlobal() bool
	Enable() bool
	SetEnable(bool)
}

type BaseDto struct {
	Switch bool `json:"switch"`
	Global bool `json:"global"`
}

func (dto *BaseDto) Enable() bool {
	return dto.Switch
}

func (dto *BaseDto) SetEnable(toggle bool) {
	dto.Switch = toggle
}

func (dto *BaseDto) SetGlobal(isGlobal bool) {
	dto.Global = isGlobal
}

func (dto BaseDto) IsGlobal() bool {
	return dto.Global
}

type BasePolicy struct {
	PolicyName string
}

func (policy BasePolicy) MergeDiceConfig(map[string]interface{}) (PolicyDto, error) {
	return nil, nil
}

func (policy BasePolicy) GetGatewayProvider(clusterName string) (string, error) {
	if clusterName == "" {
		return "", errors.Errorf("clusterName is nil")
	}
	azDb, err := db.NewGatewayAzInfoServiceImpl()
	if err != nil {
		return "", err
	}
	_, azInfo, err := azDb.GetAzInfoByClusterName(clusterName)
	if err != nil {
		return "", err
	}

	if azInfo != nil && azInfo.GatewayProvider != "" {
		return azInfo.GatewayProvider, nil
	}

	return "", nil
}

func (policy BasePolicy) GetConfig(name, packageId string, zone *orm.GatewayZone, ctx map[string]interface{}) (PolicyDto, error) {
	engine, err := GetPolicyEngine(name)
	if err != nil {
		return nil, err
	}
	policyDb, err := db.NewGatewayIngressPolicyServiceImpl()
	if err != nil {
		return nil, err
	}
	var policyConfig []byte
	gatewayProvider := ""
	useDefault := false
	if zone != nil {
		policyDao, err := policyDb.GetByAny(&orm.GatewayIngressPolicy{
			Name:   name,
			ZoneId: zone.Id,
			Az:     zone.DiceClusterName,
		})
		if err != nil {
			return nil, err
		}
		if policyDao != nil && len(policyDao.Config) > 0 {
			policyConfig = policyDao.Config
			gatewayProvider, _ = policy.GetGatewayProvider(zone.DiceClusterName)
			goto done
		}
	}
	{
		defaultPolicyDb, err := db.NewGatewayDefaultPolicyServiceImpl()
		if err != nil {
			return nil, err
		}
		defaultPolicy, err := defaultPolicyDb.GetByAny(&orm.GatewayDefaultPolicy{
			Level:     orm.POLICY_PACKAGE_LEVEL,
			Name:      name,
			PackageId: packageId,
		})
		if err != nil {
			return nil, err
		}
		if defaultPolicy == nil || len(defaultPolicy.Config) == 0 {
			dto := engine.CreateDefaultConfig(ctx)
			dto.SetGlobal(true)
			return dto, nil
		}
		policyConfig = defaultPolicy.Config
		useDefault = true
	}
done:
	dto, err, _ := engine.UnmarshalConfig(policyConfig, gatewayProvider)
	if err != nil {
		log.Errorf("unmarshal config failed, confg:%s, err:%+v", policyConfig, err)
		return nil, err
	}
	dto.SetGlobal(useDefault)
	return dto, nil
}

func (policy BasePolicy) CreateDefaultConfig(map[string]interface{}) interface{} {
	return nil
}

func (policy *BasePolicy) SetName(name string) {
	policy.PolicyName = name
}

func (policy BasePolicy) GetName() string {
	return policy.PolicyName
}

func (policy BasePolicy) NeedResetAnnotation(dto PolicyDto) bool {
	return !dto.Enable()
}

func (policy BasePolicy) NeedSerialUpdate() bool {
	return false
}
func (policy BasePolicy) ParseConfig(interface{}, map[string]interface{}) (PolicyConfig, error) {
	return PolicyConfig{}, nil
}

func (policy BasePolicy) UnmarshalConfig([]byte) (interface{}, error, string) {
	return nil, nil, ""
}

func (policy BasePolicy) GetGatewayAdapter(ctx map[string]interface{}, policyName string) (gatewayAdapter interface{}, gatewayProvider string, err error) {
	gatewayAdapter, ok := ctx[CTX_KONG_ADAPTER]
	if !ok {
		gatewayAdapter, ok = ctx[CTX_MSE_ADAPTER]
		if !ok {
			errMsg := "convert failed: can not get gateway adapter from ctx"
			log.Errorf(errMsg)
			return nil, "", errors.Errorf(errMsg)
		}
		log.Debugf("use MSE gateway ParseConfig for policy %s", policyName)
		gatewayProvider = mseCommon.MseProviderName
	} else {
		log.Debugf("use Kong gateway ParseConfig for policy %s", policyName)
	}
	return gatewayAdapter, gatewayProvider, nil
}
