package azurerm

import (
	"bytes"
	"fmt"
	"log"

	"time"

	"github.com/Azure/azure-sdk-for-go/arm/compute"
	"github.com/hashicorp/terraform/helper/hashcode"
	"github.com/hashicorp/terraform/helper/resource"
	"github.com/hashicorp/terraform/helper/schema"
)

func resourceArmVirtualMachineScaleSet() *schema.Resource {
	return &schema.Resource{
		Create: resourceArmVirtualMachineScaleSetCreate,
		Read:   resourceArmVirtualMachineScaleSetRead,
		Update: resourceArmVirtualMachineScaleSetCreate,
		Delete: resourceArmVirtualMachineScaleSetDelete,

		Schema: map[string]*schema.Schema{
			"name": &schema.Schema{
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},

			"location": &schema.Schema{
				Type:      schema.TypeString,
				Required:  true,
				ForceNew:  true,
				StateFunc: azureRMNormalizeLocation,
			},

			"resource_group_name": &schema.Schema{
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},

			"sku": &schema.Schema{
				Type:     schema.TypeSet,
				Required: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"name": &schema.Schema{
							Type:     schema.TypeString,
							Required: true,
						},

						"tier": &schema.Schema{
							Type:     schema.TypeString,
							Optional: true,
							Computed: true,
						},

						"capacity": &schema.Schema{
							Type:     schema.TypeInt,
							Required: true,
						},
					},
				},
				Set: resourceArmVirtualMachineScaleSetSkuHash,
			},

			"upgrade_policy_mode": &schema.Schema{
				Type:     schema.TypeString,
				Required: true,
			},

			"virtual_machine_network_profile": &schema.Schema{
				Type:     schema.TypeSet,
				Required: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"name": &schema.Schema{
							Type:     schema.TypeString,
							Required: true,
						},

						"primary": &schema.Schema{
							Type:     schema.TypeBool,
							Required: true,
						},

						"ip_configuration": &schema.Schema{
							Type:     schema.TypeList,
							Required: true,
							Elem: &schema.Resource{
								Schema: map[string]*schema.Schema{
									"name": &schema.Schema{
										Type:     schema.TypeString,
										Required: true,
									},

									"subnet_id": &schema.Schema{
										Type:     schema.TypeString,
										Required: true,
									},

									"load_balancer_backend_address_pool_ids": &schema.Schema{
										Type:     schema.TypeSet,
										Optional: true,
										Computed: true,
										Elem:     &schema.Schema{Type: schema.TypeString},
										Set:      schema.HashString,
									},
								},
							},
						},
					},
				},
				Set: resourceArmVirtualMachineScaleSetNetworkConfigurationHash,
			},

			"virtual_machine_storage_profile_os_disk": &schema.Schema{
				Type:     schema.TypeSet,
				Required: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"name": &schema.Schema{
							Type:     schema.TypeString,
							Required: true,
						},

						"image": &schema.Schema{
							Type:     schema.TypeString,
							Optional: true,
							Computed: true,
						},

						"vhd_containers": &schema.Schema{
							Type:     schema.TypeSet,
							Required: true,
							Elem:     &schema.Schema{Type: schema.TypeString},
							Set:      schema.HashString,
						},

						"caching": &schema.Schema{
							Type:     schema.TypeString,
							Required: true,
						},

						"os_type": &schema.Schema{
							Type:     schema.TypeString,
							Required: true,
						},

						"create_option": &schema.Schema{
							Type:     schema.TypeString,
							Required: true,
						},
					},
				},
				Set: resourceArmVirtualMachineScaleSetStorageProfileOsDiskHash,
			},

			"tags": tagsSchema(),
		},
	}
}

func resourceArmVirtualMachineScaleSetCreate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*ArmClient)
	vmScaleSetClient := client.vmScaleSetClient

	log.Printf("[INFO] preparing arguments for Azure ARM Virtual Machine Scale Set creation.")

	name := d.Get("name").(string)
	location := d.Get("location").(string)
	resGroup := d.Get("resource_group_name").(string)
	tags := d.Get("tags").(map[string]interface{})

	sku, err := expandVirtualMachineScaleSetPlan(d)
	if err != nil {
		return err
	}

	updatePolicy := d.Get("upgrade_policy_mode").(string)
	scaleSetProps := compute.VirtualMachineScaleSetProperties{
		UpgradePolicy: &compute.UpgradePolicy{
			Mode: compute.UpgradeMode(updatePolicy),
		},
		VirtualMachineProfile: &compute.VirtualMachineScaleSetVMProfile{
			NetworkProfile: expandAzureRmVirtualMachineScaleSetNetworkProfile(d),
		},
	}

	scaleSetParams := compute.VirtualMachineScaleSet{
		Name:       &name,
		Location:   &location,
		Tags:       expandTags(tags),
		Sku:        sku,
		Properties: &scaleSetProps,
	}
	resp, vmErr := vmScaleSetClient.CreateOrUpdate(resGroup, name, scaleSetParams)
	if vmErr != nil {
		return vmErr
	}

	d.SetId(*resp.ID)

	log.Printf("[DEBUG] Waiting for Virtual Machine ScaleSet (%s) to become available", name)
	stateConf := &resource.StateChangeConf{
		Pending:    []string{"Creating", "Updating"},
		Target:     []string{"Succeeded"},
		Refresh:    virtualMachineScaleSetStateRefreshFunc(client, resGroup, name),
		Timeout:    20 * time.Minute,
		MinTimeout: 10 * time.Second,
	}
	if _, err := stateConf.WaitForState(); err != nil {
		return fmt.Errorf("Error waiting for Virtual Machine Scale Set (%s) to become available: %s", name, err)
	}

	return nil
}

func resourceArmVirtualMachineScaleSetRead(d *schema.ResourceData, meta interface{}) error {
	return nil
}

func resourceArmVirtualMachineScaleSetDelete(d *schema.ResourceData, meta interface{}) error {
	vmScaleSetClient := meta.(*ArmClient).vmScaleSetClient

	id, err := parseAzureResourceID(d.Id())
	if err != nil {
		return err
	}
	resGroup := id.ResourceGroup
	name := id.Path["VirtualMachineScaleSets"]

	_, err = vmScaleSetClient.Delete(resGroup, name)

	return err
}

func virtualMachineScaleSetStateRefreshFunc(client *ArmClient, resourceGroupName string, scaleSetName string) resource.StateRefreshFunc {
	return func() (interface{}, string, error) {
		res, err := client.vmScaleSetClient.Get(resourceGroupName, scaleSetName)
		if err != nil {
			return nil, "", fmt.Errorf("Error issuing read request in virtualMachineScaleSetStateRefreshFunc to Azure ARM for Virtual Machine Scale Set '%s' (RG: '%s'): %s", scaleSetName, resourceGroupName, err)
		}

		return res, *res.Properties.ProvisioningState, nil
	}
}

func resourceArmVirtualMachineScaleSetSkuHash(v interface{}) int {
	var buf bytes.Buffer
	m := v.(map[string]interface{})
	buf.WriteString(fmt.Sprintf("%s-", m["name"].(string)))
	if m["tier"] != nil {
		buf.WriteString(fmt.Sprintf("%s-", m["tier"].(string)))
	}
	buf.WriteString(fmt.Sprintf("%d-", m["capacity"].(int)))

	return hashcode.String(buf.String())
}

func resourceArmVirtualMachineScaleSetStorageProfileOsDiskHash(v interface{}) int {
	var buf bytes.Buffer
	m := v.(map[string]interface{})
	buf.WriteString(fmt.Sprintf("%s-", m["name"].(string)))
	buf.WriteString(fmt.Sprintf("%s-", m["caching"].(string)))
	buf.WriteString(fmt.Sprintf("%s-", m["os_type"].(string)))
	buf.WriteString(fmt.Sprintf("%s-", m["create_option"].(string)))
	if m["image"] != nil {
		buf.WriteString(fmt.Sprintf("%s-", m["image"].(string)))
	}

	return hashcode.String(buf.String())
}

func resourceArmVirtualMachineScaleSetNetworkConfigurationHash(v interface{}) int {
	var buf bytes.Buffer
	m := v.(map[string]interface{})
	buf.WriteString(fmt.Sprintf("%s-", m["name"].(string)))
	buf.WriteString(fmt.Sprintf("%t-", m["primary"].(bool)))
	return hashcode.String(buf.String())
}

func expandVirtualMachineScaleSetPlan(d *schema.ResourceData) (*compute.Sku, error) {
	skuConfig := d.Get("sku").(*schema.Set).List()

	if len(skuConfig) != 1 {
		return nil, fmt.Errorf("Cannot specify more than one SKU.")
	}

	config := skuConfig[0].(map[string]interface{})

	name := config["name"].(string)
	tier := config["tier"].(string)
	capacity := int32(config["capacity"].(int))

	sku := &compute.Sku{
		Name:     &name,
		Capacity: &capacity,
	}

	if tier != "" {
		sku.Tier = &tier
	}

	return sku, nil
}

func expandAzureRmVirtualMachineScaleSetNetworkProfile(d *schema.ResourceData) *compute.VirtualMachineScaleSetNetworkProfile {
	scaleSetNetworkProfileConfigs := d.Get("virtual_machine_network_profile").(*schema.Set).List()
	networkProfileConfig := make([]compute.VirtualMachineScaleSetNetworkConfiguration, 0, len(scaleSetNetworkProfileConfigs))

	for _, npProfileConfig := range scaleSetNetworkProfileConfigs {
		config := npProfileConfig.(map[string]interface{})

		name := config["name"].(string)
		primary := config["primary"].(bool)

		ipConfigurationConfigs := config["ip_configuration"].([]interface{})
		ipConfigurations := make([]compute.VirtualMachineScaleSetIPConfiguration, 0, len(ipConfigurationConfigs))
		for _, ipConfigConfig := range ipConfigurationConfigs {
			ipconfig := ipConfigConfig.(map[string]interface{})
			name := ipconfig["name"].(string)
			subnetId := ipconfig["subnet_id"].(string)

			ipConfiguration := compute.VirtualMachineScaleSetIPConfiguration{
				Name: &name,
				Properties: &compute.VirtualMachineScaleSetIPConfigurationProperties{
					Subnet: &compute.APIEntityReference{
						ID: &subnetId,
					},
				},
			}

			//if v := ipconfig["load_balancer_backend_address_pool_ids"]; v != nil {
			//
			//}

			ipConfigurations = append(ipConfigurations, ipConfiguration)
		}

		nProfile := compute.VirtualMachineScaleSetNetworkConfiguration{
			Name: &name,
			Properties: &compute.VirtualMachineScaleSetNetworkConfigurationProperties{
				Primary:          &primary,
				IPConfigurations: &ipConfigurations,
			},
		}

		networkProfileConfig = append(networkProfileConfig, nProfile)
	}

	return &compute.VirtualMachineScaleSetNetworkProfile{
		NetworkInterfaceConfigurations: &networkProfileConfig,
	}
}
