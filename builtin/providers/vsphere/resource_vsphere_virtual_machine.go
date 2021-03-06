package vsphere

import (
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform/helper/resource"
	"github.com/hashicorp/terraform/helper/schema"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
	"golang.org/x/net/context"
)

var DefaultDNSSuffixes = []string{
	"vsphere.local",
}

var DefaultDNSServers = []string{
	"8.8.8.8",
	"8.8.4.4",
}

type networkInterface struct {
	deviceName       string
	label            string
	ipv4Address      string
	ipv4PrefixLength int
	ipv4Gateway      string
	ipv6Address      string
	ipv6PrefixLength int
	ipv6Gateway      string
	adapterType      string // TODO: Make "adapter_type" argument
}

type hardDisk struct {
	size     int64
	iops     int64
	initType string
	vmdkPath string
}

//Additional options Vsphere can use clones of windows machines
type windowsOptConfig struct {
	productKey         string
	adminPassword      string
	domainUser         string
	domain             string
	domainUserPassword string
}

type cdrom struct {
	datastore string
	path      string
}

type memoryAllocation struct {
	reservation int64
}

type virtualMachine struct {
	name                  string
	folder                string
	datacenter            string
	cluster               string
	resourcePool          string
	datastore             string
	vcpu                  int
	memoryMb              int64
	memoryAllocation      memoryAllocation
	template              string
	networkInterfaces     []networkInterface
	hardDisks             []hardDisk
	cdroms                []cdrom
	domain                string
	timeZone              string
	dnsSuffixes           []string
	dnsServers            []string
	bootableVmdk          bool
	linkedClone           bool
	skipCustomization     bool
	windowsOptionalConfig windowsOptConfig
	customConfigurations  map[string](types.AnyType)
}

func (v virtualMachine) Path() string {
	return vmPath(v.folder, v.name)
}

func vmPath(folder string, name string) string {
	var path string
	if len(folder) > 0 {
		path += folder + "/"
	}
	return path + name
}

func resourceVSphereVirtualMachine() *schema.Resource {
	return &schema.Resource{
		Create: resourceVSphereVirtualMachineCreate,
		Read:   resourceVSphereVirtualMachineRead,
		Update: resourceVSphereVirtualMachineUpdate,
		Delete: resourceVSphereVirtualMachineDelete,

		Schema: map[string]*schema.Schema{
			"name": &schema.Schema{
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},

			"folder": &schema.Schema{
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
			},

			"vcpu": &schema.Schema{
				Type:     schema.TypeInt,
				Required: true,
			},

			"memory": &schema.Schema{
				Type:     schema.TypeInt,
				Required: true,
			},

			"memory_reservation": &schema.Schema{
				Type:     schema.TypeInt,
				Optional: true,
				Default:  0,
				ForceNew: true,
			},

			"datacenter": &schema.Schema{
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
			},

			"cluster": &schema.Schema{
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
			},

			"resource_pool": &schema.Schema{
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
			},

			"linked_clone": &schema.Schema{
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
				ForceNew: true,
			},
			"gateway": &schema.Schema{
				Type:       schema.TypeString,
				Optional:   true,
				ForceNew:   true,
				Deprecated: "Please use network_interface.ipv4_gateway",
			},

			"domain": &schema.Schema{
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
				Default:  "vsphere.local",
			},

			"time_zone": &schema.Schema{
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
				Default:  "Etc/UTC",
			},

			"dns_suffixes": &schema.Schema{
				Type:     schema.TypeList,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
				ForceNew: true,
			},

			"dns_servers": &schema.Schema{
				Type:     schema.TypeList,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
				ForceNew: true,
			},

			"skip_customization": &schema.Schema{
				Type:     schema.TypeBool,
				Optional: true,
				ForceNew: true,
				Default:  false,
			},

			"custom_configuration_parameters": &schema.Schema{
				Type:     schema.TypeMap,
				Optional: true,
				ForceNew: true,
			},
			"windows_opt_config": &schema.Schema{
				Type:     schema.TypeList,
				Optional: true,
				ForceNew: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"product_key": &schema.Schema{
							Type:     schema.TypeString,
							Required: true,
							ForceNew: true,
						},

						"admin_password": &schema.Schema{
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
						},

						"domain_user": &schema.Schema{
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
						},

						"domain": &schema.Schema{
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
						},

						"domain_user_password": &schema.Schema{
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
						},
					},
				},
			},

			"network_interface": &schema.Schema{
				Type:     schema.TypeList,
				Required: true,
				ForceNew: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"label": &schema.Schema{
							Type:     schema.TypeString,
							Required: true,
							ForceNew: true,
						},

						"ip_address": &schema.Schema{
							Type:       schema.TypeString,
							Optional:   true,
							Computed:   true,
							Deprecated: "Please use ipv4_address",
						},

						"subnet_mask": &schema.Schema{
							Type:       schema.TypeString,
							Optional:   true,
							Computed:   true,
							Deprecated: "Please use ipv4_prefix_length",
						},

						"ipv4_address": &schema.Schema{
							Type:     schema.TypeString,
							Optional: true,
							Computed: true,
						},

						"ipv4_prefix_length": &schema.Schema{
							Type:     schema.TypeInt,
							Optional: true,
							Computed: true,
						},

						"ipv4_gateway": &schema.Schema{
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
						},

						"ipv6_address": &schema.Schema{
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
						},

						"ipv6_prefix_length": &schema.Schema{
							Type:     schema.TypeInt,
							Optional: true,
							ForceNew: true,
						},

						"ipv6_gateway": &schema.Schema{
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
						},

						"adapter_type": &schema.Schema{
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
						},
					},
				},
			},

			"disk": &schema.Schema{
				Type:     schema.TypeList,
				Required: true,
				ForceNew: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"template": &schema.Schema{
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
						},

						"type": &schema.Schema{
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
							Default:  "eager_zeroed",
							ValidateFunc: func(v interface{}, k string) (ws []string, errors []error) {
								value := v.(string)
								if value != "thin" && value != "eager_zeroed" {
									errors = append(errors, fmt.Errorf(
										"only 'thin' and 'eager_zeroed' are supported values for 'type'"))
								}
								return
							},
						},

						"datastore": &schema.Schema{
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
						},

						"size": &schema.Schema{
							Type:     schema.TypeInt,
							Optional: true,
							ForceNew: true,
						},

						"iops": &schema.Schema{
							Type:     schema.TypeInt,
							Optional: true,
							ForceNew: true,
						},

						"vmdk": &schema.Schema{
							// TODO: Add ValidateFunc to confirm path exists
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
							Default:  "",
						},

						"bootable": &schema.Schema{
							Type:     schema.TypeBool,
							Optional: true,
							Default:  false,
							ForceNew: true,
						},
					},
				},
			},

			"cdrom": &schema.Schema{
				Type:     schema.TypeList,
				Optional: true,
				ForceNew: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"datastore": &schema.Schema{
							Type:     schema.TypeString,
							Required: true,
							ForceNew: true,
						},

						"path": &schema.Schema{
							Type:     schema.TypeString,
							Required: true,
							ForceNew: true,
						},
					},
				},
			},

			"boot_delay": &schema.Schema{
				Type:     schema.TypeInt,
				Optional: true,
				ForceNew: true,
			},
		},
	}
}

func resourceVSphereVirtualMachineUpdate(d *schema.ResourceData, meta interface{}) error {
	// flag if changes have to be applied
	hasChanges := false
	// flag if changes have to be done when powered off
	rebootRequired := false

	// make config spec
	configSpec := types.VirtualMachineConfigSpec{}

	if d.HasChange("vcpu") {
		configSpec.NumCPUs = d.Get("vcpu").(int)
		hasChanges = true
		rebootRequired = true
	}

	if d.HasChange("memory") {
		configSpec.MemoryMB = int64(d.Get("memory").(int))
		hasChanges = true
		rebootRequired = true
	}

	// do nothing if there are no changes
	if !hasChanges {
		return nil
	}

	client := meta.(*govmomi.Client)
	dc, err := getDatacenter(client, d.Get("datacenter").(string))
	if err != nil {
		return err
	}
	finder := find.NewFinder(client.Client, true)
	finder = finder.SetDatacenter(dc)

	vm, err := finder.VirtualMachine(context.TODO(), vmPath(d.Get("folder").(string), d.Get("name").(string)))
	if err != nil {
		return err
	}
	log.Printf("[DEBUG] virtual machine config spec: %v", configSpec)

	if rebootRequired {
		log.Printf("[INFO] Shutting down virtual machine: %s", d.Id())

		task, err := vm.PowerOff(context.TODO())
		if err != nil {
			return err
		}

		err = task.Wait(context.TODO())
		if err != nil {
			return err
		}
	}

	log.Printf("[INFO] Reconfiguring virtual machine: %s", d.Id())

	task, err := vm.Reconfigure(context.TODO(), configSpec)
	if err != nil {
		log.Printf("[ERROR] %s", err)
	}

	err = task.Wait(context.TODO())
	if err != nil {
		log.Printf("[ERROR] %s", err)
	}

	if rebootRequired {
		task, err = vm.PowerOn(context.TODO())
		if err != nil {
			return err
		}

		err = task.Wait(context.TODO())
		if err != nil {
			log.Printf("[ERROR] %s", err)
		}
	}

	ip, err := vm.WaitForIP(context.TODO())
	if err != nil {
		return err
	}
	log.Printf("[DEBUG] ip address: %v", ip)

	return resourceVSphereVirtualMachineRead(d, meta)
}

func resourceVSphereVirtualMachineCreate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*govmomi.Client)

	vm := virtualMachine{
		name:     d.Get("name").(string),
		vcpu:     d.Get("vcpu").(int),
		memoryMb: int64(d.Get("memory").(int)),
		memoryAllocation: memoryAllocation{
			reservation: int64(d.Get("memory_reservation").(int)),
		},
	}

	if v, ok := d.GetOk("folder"); ok {
		vm.folder = v.(string)
	}

	if v, ok := d.GetOk("datacenter"); ok {
		vm.datacenter = v.(string)
	}

	if v, ok := d.GetOk("cluster"); ok {
		vm.cluster = v.(string)
	}

	if v, ok := d.GetOk("resource_pool"); ok {
		vm.resourcePool = v.(string)
	}

	if v, ok := d.GetOk("domain"); ok {
		vm.domain = v.(string)
	}

	if v, ok := d.GetOk("time_zone"); ok {
		vm.timeZone = v.(string)
	}

	if v, ok := d.GetOk("linked_clone"); ok {
		vm.linkedClone = v.(bool)
	}

	if v, ok := d.GetOk("skip_customization"); ok {
		vm.skipCustomization = v.(bool)
	}

	if raw, ok := d.GetOk("dns_suffixes"); ok {
		for _, v := range raw.([]interface{}) {
			vm.dnsSuffixes = append(vm.dnsSuffixes, v.(string))
		}
	} else {
		vm.dnsSuffixes = DefaultDNSSuffixes
	}

	if raw, ok := d.GetOk("dns_servers"); ok {
		for _, v := range raw.([]interface{}) {
			vm.dnsServers = append(vm.dnsServers, v.(string))
		}
	} else {
		vm.dnsServers = DefaultDNSServers
	}

	if vL, ok := d.GetOk("custom_configuration_parameters"); ok {
		if custom_configs, ok := vL.(map[string]interface{}); ok {
			custom := make(map[string]types.AnyType)
			for k, v := range custom_configs {
				custom[k] = v
			}
			vm.customConfigurations = custom
			log.Printf("[DEBUG] custom_configuration_parameters init: %v", vm.customConfigurations)
		}
	}

	if vL, ok := d.GetOk("network_interface"); ok {
		networks := make([]networkInterface, len(vL.([]interface{})))
		for i, v := range vL.([]interface{}) {
			network := v.(map[string]interface{})
			networks[i].label = network["label"].(string)
			if v, ok := network["ip_address"].(string); ok && v != "" {
				networks[i].ipv4Address = v
			}
			if v, ok := d.GetOk("gateway"); ok {
				networks[i].ipv4Gateway = v.(string)
			}
			if v, ok := network["subnet_mask"].(string); ok && v != "" {
				ip := net.ParseIP(v).To4()
				if ip != nil {
					mask := net.IPv4Mask(ip[0], ip[1], ip[2], ip[3])
					pl, _ := mask.Size()
					networks[i].ipv4PrefixLength = pl
				} else {
					return fmt.Errorf("subnet_mask parameter is invalid.")
				}
			}
			if v, ok := network["ipv4_address"].(string); ok && v != "" {
				networks[i].ipv4Address = v
			}
			if v, ok := network["ipv4_prefix_length"].(int); ok && v != 0 {
				networks[i].ipv4PrefixLength = v
			}
			if v, ok := network["ipv4_gateway"].(string); ok && v != "" {
				networks[i].ipv4Gateway = v
			}
			if v, ok := network["ipv6_address"].(string); ok && v != "" {
				networks[i].ipv6Address = v
			}
			if v, ok := network["ipv6_prefix_length"].(int); ok && v != 0 {
				networks[i].ipv6PrefixLength = v
			}
			if v, ok := network["ipv6_gateway"].(string); ok && v != "" {
				networks[i].ipv6Gateway = v
			}
		}
		vm.networkInterfaces = networks
		log.Printf("[DEBUG] network_interface init: %v", networks)
	}

	if vL, ok := d.GetOk("windows_opt_config"); ok {
		var winOpt windowsOptConfig
		custom_configs := (vL.([]interface{}))[0].(map[string]interface{})
		if v, ok := custom_configs["admin_password"].(string); ok && v != "" {
			winOpt.adminPassword = v
		}
		if v, ok := custom_configs["domain"].(string); ok && v != "" {
			winOpt.domain = v
		}
		if v, ok := custom_configs["domain_user"].(string); ok && v != "" {
			winOpt.domainUser = v
		}
		if v, ok := custom_configs["product_key"].(string); ok && v != "" {
			winOpt.productKey = v
		}
		if v, ok := custom_configs["domain_user_password"].(string); ok && v != "" {
			winOpt.domainUserPassword = v
		}
		vm.windowsOptionalConfig = winOpt
		log.Printf("[DEBUG] windows config init: %v", winOpt)
	}

	if vL, ok := d.GetOk("disk"); ok {
		disks := make([]hardDisk, len(vL.([]interface{})))
		for i, v := range vL.([]interface{}) {
			disk := v.(map[string]interface{})
			if i == 0 {
				if v, ok := disk["template"].(string); ok && v != "" {
					vm.template = v
				} else {
					if v, ok := disk["size"].(int); ok && v != 0 {
						disks[i].size = int64(v)
					} else if v, ok := disk["vmdk"].(string); ok && v != "" {
						disks[i].vmdkPath = v
						if v, ok := disk["bootable"].(bool); ok {
							vm.bootableVmdk = v
						}
					} else {
						return fmt.Errorf("template, size, or vmdk argument is required")
					}
				}
				if v, ok := disk["datastore"].(string); ok && v != "" {
					vm.datastore = v
				}
			} else {
				if v, ok := disk["size"].(int); ok && v != 0 {
					disks[i].size = int64(v)
				} else if v, ok := disk["vmdk"].(string); ok && v != "" {
					disks[i].vmdkPath = v
				} else {
					return fmt.Errorf("size or vmdk argument is required")
				}

			}
			if v, ok := disk["iops"].(int); ok && v != 0 {
				disks[i].iops = int64(v)
			}
			if v, ok := disk["type"].(string); ok && v != "" {
				disks[i].initType = v
			}
		}
		vm.hardDisks = disks
		log.Printf("[DEBUG] disk init: %v", disks)
	}

	if vL, ok := d.GetOk("cdrom"); ok {
		cdroms := make([]cdrom, len(vL.([]interface{})))
		for i, v := range vL.([]interface{}) {
			c := v.(map[string]interface{})
			if v, ok := c["datastore"].(string); ok && v != "" {
				cdroms[i].datastore = v
			} else {
				return fmt.Errorf("Datastore argument must be specified when attaching a cdrom image.")
			}
			if v, ok := c["path"].(string); ok && v != "" {
				cdroms[i].path = v
			} else {
				return fmt.Errorf("Path argument must be specified when attaching a cdrom image.")
			}
		}
		vm.cdroms = cdroms
		log.Printf("[DEBUG] cdrom init: %v", cdroms)
	}

	if vm.template != "" {
		err := vm.deployVirtualMachine(client)
		if err != nil {
			return err
		}
	} else {
		err := vm.createVirtualMachine(client)
		if err != nil {
			return err
		}
	}

	if _, ok := d.GetOk("network_interface.0.ipv4_address"); !ok {
		if v, ok := d.GetOk("boot_delay"); ok {
			stateConf := &resource.StateChangeConf{
				Pending:    []string{"pending"},
				Target:     []string{"active"},
				Refresh:    waitForNetworkingActive(client, vm.datacenter, vm.Path()),
				Timeout:    600 * time.Second,
				Delay:      time.Duration(v.(int)) * time.Second,
				MinTimeout: 2 * time.Second,
			}

			_, err := stateConf.WaitForState()
			if err != nil {
				return err
			}
		}
	}

	if ip, ok := d.GetOk("network_interface.0.ipv4_address"); ok {
		d.SetConnInfo(map[string]string{
			"host": ip.(string),
		})
	} else {
		log.Printf("[DEBUG] Could not get IP address for %s", d.Id())
	}

	d.SetId(vm.Path())
	log.Printf("[INFO] Created virtual machine: %s", d.Id())

	return resourceVSphereVirtualMachineRead(d, meta)
}

func resourceVSphereVirtualMachineRead(d *schema.ResourceData, meta interface{}) error {
	log.Printf("[DEBUG] reading virtual machine: %#v", d)
	client := meta.(*govmomi.Client)
	dc, err := getDatacenter(client, d.Get("datacenter").(string))
	if err != nil {
		return err
	}
	finder := find.NewFinder(client.Client, true)
	finder = finder.SetDatacenter(dc)

	vm, err := finder.VirtualMachine(context.TODO(), d.Id())
	if err != nil {
		d.SetId("")
		return nil
	}

	var mvm mo.VirtualMachine

	collector := property.DefaultCollector(client.Client)
	if err := collector.RetrieveOne(context.TODO(), vm.Reference(), []string{"guest", "summary", "datastore"}, &mvm); err != nil {
		return err
	}

	log.Printf("[DEBUG] %#v", dc)
	log.Printf("[DEBUG] %#v", mvm.Summary.Config)
	log.Printf("[DEBUG] %#v", mvm.Guest.Net)

	networkInterfaces := make([]map[string]interface{}, 0)
	for _, v := range mvm.Guest.Net {
		if v.DeviceConfigId >= 0 {
			log.Printf("[DEBUG] %#v", v.Network)
			networkInterface := make(map[string]interface{})
			networkInterface["label"] = v.Network
			for _, ip := range v.IpConfig.IpAddress {
				p := net.ParseIP(ip.IpAddress)
				if p.To4() != nil {
					log.Printf("[DEBUG] %#v", p.String())
					log.Printf("[DEBUG] %#v", ip.PrefixLength)
					networkInterface["ipv4_address"] = p.String()
					networkInterface["ipv4_prefix_length"] = ip.PrefixLength
				} else if p.To16() != nil {
					log.Printf("[DEBUG] %#v", p.String())
					log.Printf("[DEBUG] %#v", ip.PrefixLength)
					networkInterface["ipv6_address"] = p.String()
					networkInterface["ipv6_prefix_length"] = ip.PrefixLength
				}
				log.Printf("[DEBUG] networkInterface: %#v", networkInterface)
			}
			log.Printf("[DEBUG] networkInterface: %#v", networkInterface)
			networkInterfaces = append(networkInterfaces, networkInterface)
		}
	}
	log.Printf("[DEBUG] networkInterfaces: %#v", networkInterfaces)
	err = d.Set("network_interface", networkInterfaces)
	if err != nil {
		return fmt.Errorf("Invalid network interfaces to set: %#v", networkInterfaces)
	}

	ip, err := vm.WaitForIP(context.TODO())
	if err != nil {
		return err
	}
	log.Printf("[DEBUG] ip address: %v", ip)
	d.SetConnInfo(map[string]string{
		"type": "ssh",
		"host": ip,
	})

	var rootDatastore string
	for _, v := range mvm.Datastore {
		var md mo.Datastore
		if err := collector.RetrieveOne(context.TODO(), v, []string{"name", "parent"}, &md); err != nil {
			return err
		}
		if md.Parent.Type == "StoragePod" {
			var msp mo.StoragePod
			if err := collector.RetrieveOne(context.TODO(), *md.Parent, []string{"name"}, &msp); err != nil {
				return err
			}
			rootDatastore = msp.Name
			log.Printf("[DEBUG] %#v", msp.Name)
		} else {
			rootDatastore = md.Name
			log.Printf("[DEBUG] %#v", md.Name)
		}
		break
	}

	d.Set("datacenter", dc)
	d.Set("memory", mvm.Summary.Config.MemorySizeMB)
	d.Set("memory_reservation", mvm.Summary.Config.MemoryReservation)
	d.Set("cpu", mvm.Summary.Config.NumCpu)
	d.Set("datastore", rootDatastore)

	return nil
}

func resourceVSphereVirtualMachineDelete(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*govmomi.Client)
	dc, err := getDatacenter(client, d.Get("datacenter").(string))
	if err != nil {
		return err
	}
	finder := find.NewFinder(client.Client, true)
	finder = finder.SetDatacenter(dc)

	vm, err := finder.VirtualMachine(context.TODO(), vmPath(d.Get("folder").(string), d.Get("name").(string)))
	if err != nil {
		return err
	}

	log.Printf("[INFO] Deleting virtual machine: %s", d.Id())
	state, err := vm.PowerState(context.TODO())
	if err != nil {
		return err
	}

	if state == types.VirtualMachinePowerStatePoweredOn {
		task, err := vm.PowerOff(context.TODO())
		if err != nil {
			return err
		}

		err = task.Wait(context.TODO())
		if err != nil {
			return err
		}
	}

	task, err := vm.Destroy(context.TODO())
	if err != nil {
		return err
	}

	err = task.Wait(context.TODO())
	if err != nil {
		return err
	}

	d.SetId("")
	return nil
}

func waitForNetworkingActive(client *govmomi.Client, datacenter, name string) resource.StateRefreshFunc {
	return func() (interface{}, string, error) {
		dc, err := getDatacenter(client, datacenter)
		if err != nil {
			log.Printf("[ERROR] %#v", err)
			return nil, "", err
		}
		finder := find.NewFinder(client.Client, true)
		finder = finder.SetDatacenter(dc)

		vm, err := finder.VirtualMachine(context.TODO(), name)
		if err != nil {
			log.Printf("[ERROR] %#v", err)
			return nil, "", err
		}

		var mvm mo.VirtualMachine
		collector := property.DefaultCollector(client.Client)
		if err := collector.RetrieveOne(context.TODO(), vm.Reference(), []string{"summary"}, &mvm); err != nil {
			log.Printf("[ERROR] %#v", err)
			return nil, "", err
		}

		if mvm.Summary.Guest.IpAddress != "" {
			log.Printf("[DEBUG] IP address with DHCP: %v", mvm.Summary.Guest.IpAddress)
			return mvm.Summary, "active", err
		} else {
			log.Printf("[DEBUG] Waiting for IP address")
			return nil, "pending", err
		}
	}
}

// addHardDisk adds a new Hard Disk to the VirtualMachine.
func addHardDisk(vm *object.VirtualMachine, size, iops int64, diskType string, datastore *object.Datastore, diskPath string) error {
	devices, err := vm.Device(context.TODO())
	if err != nil {
		return err
	}
	log.Printf("[DEBUG] vm devices: %#v\n", devices)

	controller, err := devices.FindDiskController("scsi")
	if err != nil {
		return err
	}
	log.Printf("[DEBUG] disk controller: %#v\n", controller)

	// If diskPath is not specified, pass empty string to CreateDisk()
	var newDiskPath string
	if diskPath == "" {
		newDiskPath = ""
	} else {
		// TODO Check if diskPath & datastore exist
		newDiskPath = fmt.Sprintf("[%v] %v", datastore.Name(), diskPath)
	}
	disk := devices.CreateDisk(controller, newDiskPath)
	existing := devices.SelectByBackingInfo(disk.Backing)
	log.Printf("[DEBUG] disk: %#v\n", disk)

	if len(existing) == 0 {
		disk.CapacityInKB = int64(size * 1024 * 1024)
		if iops != 0 {
			disk.StorageIOAllocation = &types.StorageIOAllocationInfo{
				Limit: iops,
			}
		}
		backing := disk.Backing.(*types.VirtualDiskFlatVer2BackingInfo)

		if diskType == "eager_zeroed" {
			// eager zeroed thick virtual disk
			backing.ThinProvisioned = types.NewBool(false)
			backing.EagerlyScrub = types.NewBool(true)
		} else if diskType == "thin" {
			// thin provisioned virtual disk
			backing.ThinProvisioned = types.NewBool(true)
		}

		log.Printf("[DEBUG] addHardDisk: %#v\n", disk)
		log.Printf("[DEBUG] addHardDisk: %#v\n", disk.CapacityInKB)

		return vm.AddDevice(context.TODO(), disk)
	} else {
		log.Printf("[DEBUG] addHardDisk: Disk already present.\n")

		return nil
	}
}

// addCdrom adds a new virtual cdrom drive to the VirtualMachine and attaches an image (ISO) to it from a datastore path.
func addCdrom(vm *object.VirtualMachine, datastore, path string) error {
	devices, err := vm.Device(context.TODO())
	if err != nil {
		return err
	}
	log.Printf("[DEBUG] vm devices: %#v", devices)

	controller, err := devices.FindIDEController("")
	if err != nil {
		return err
	}
	log.Printf("[DEBUG] ide controller: %#v", controller)

	c, err := devices.CreateCdrom(controller)
	if err != nil {
		return err
	}

	c = devices.InsertIso(c, fmt.Sprintf("[%s] %s", datastore, path))
	log.Printf("[DEBUG] addCdrom: %#v", c)

	return vm.AddDevice(context.TODO(), c)
}

// buildNetworkDevice builds VirtualDeviceConfigSpec for Network Device.
func buildNetworkDevice(f *find.Finder, label, adapterType string) (*types.VirtualDeviceConfigSpec, error) {
	network, err := f.Network(context.TODO(), "*"+label)
	if err != nil {
		return nil, err
	}

	backing, err := network.EthernetCardBackingInfo(context.TODO())
	if err != nil {
		return nil, err
	}

	if adapterType == "vmxnet3" {
		return &types.VirtualDeviceConfigSpec{
			Operation: types.VirtualDeviceConfigSpecOperationAdd,
			Device: &types.VirtualVmxnet3{
				VirtualVmxnet: types.VirtualVmxnet{
					VirtualEthernetCard: types.VirtualEthernetCard{
						VirtualDevice: types.VirtualDevice{
							Key:     -1,
							Backing: backing,
						},
						AddressType: string(types.VirtualEthernetCardMacTypeGenerated),
					},
				},
			},
		}, nil
	} else if adapterType == "e1000" {
		return &types.VirtualDeviceConfigSpec{
			Operation: types.VirtualDeviceConfigSpecOperationAdd,
			Device: &types.VirtualE1000{
				VirtualEthernetCard: types.VirtualEthernetCard{
					VirtualDevice: types.VirtualDevice{
						Key:     -1,
						Backing: backing,
					},
					AddressType: string(types.VirtualEthernetCardMacTypeGenerated),
				},
			},
		}, nil
	} else {
		return nil, fmt.Errorf("Invalid network adapter type.")
	}
}

// buildVMRelocateSpec builds VirtualMachineRelocateSpec to set a place for a new VirtualMachine.
func buildVMRelocateSpec(rp *object.ResourcePool, ds *object.Datastore, vm *object.VirtualMachine, linkedClone bool, initType string) (types.VirtualMachineRelocateSpec, error) {
	var key int
	var moveType string
	if linkedClone {
		moveType = "createNewChildDiskBacking"
	} else {
		moveType = "moveAllDiskBackingsAndDisallowSharing"
	}
	log.Printf("[DEBUG] relocate type: [%s]", moveType)

	devices, err := vm.Device(context.TODO())
	if err != nil {
		return types.VirtualMachineRelocateSpec{}, err
	}
	for _, d := range devices {
		if devices.Type(d) == "disk" {
			key = d.GetVirtualDevice().Key
		}
	}

	isThin := initType == "thin"
	rpr := rp.Reference()
	dsr := ds.Reference()
	return types.VirtualMachineRelocateSpec{
		Datastore:    &dsr,
		Pool:         &rpr,
		DiskMoveType: moveType,
		Disk: []types.VirtualMachineRelocateSpecDiskLocator{
			types.VirtualMachineRelocateSpecDiskLocator{
				Datastore: dsr,
				DiskBackingInfo: &types.VirtualDiskFlatVer2BackingInfo{
					DiskMode:        "persistent",
					ThinProvisioned: types.NewBool(isThin),
					EagerlyScrub:    types.NewBool(!isThin),
				},
				DiskId: key,
			},
		},
	}, nil
}

// getDatastoreObject gets datastore object.
func getDatastoreObject(client *govmomi.Client, f *object.DatacenterFolders, name string) (types.ManagedObjectReference, error) {
	s := object.NewSearchIndex(client.Client)
	ref, err := s.FindChild(context.TODO(), f.DatastoreFolder, name)
	if err != nil {
		return types.ManagedObjectReference{}, err
	}
	if ref == nil {
		return types.ManagedObjectReference{}, fmt.Errorf("Datastore '%s' not found.", name)
	}
	log.Printf("[DEBUG] getDatastoreObject: reference: %#v", ref)
	return ref.Reference(), nil
}

// buildStoragePlacementSpecCreate builds StoragePlacementSpec for create action.
func buildStoragePlacementSpecCreate(f *object.DatacenterFolders, rp *object.ResourcePool, storagePod object.StoragePod, configSpec types.VirtualMachineConfigSpec) types.StoragePlacementSpec {
	vmfr := f.VmFolder.Reference()
	rpr := rp.Reference()
	spr := storagePod.Reference()

	sps := types.StoragePlacementSpec{
		Type:       "create",
		ConfigSpec: &configSpec,
		PodSelectionSpec: types.StorageDrsPodSelectionSpec{
			StoragePod: &spr,
		},
		Folder:       &vmfr,
		ResourcePool: &rpr,
	}
	log.Printf("[DEBUG] findDatastore: StoragePlacementSpec: %#v\n", sps)
	return sps
}

// buildStoragePlacementSpecClone builds StoragePlacementSpec for clone action.
func buildStoragePlacementSpecClone(c *govmomi.Client, f *object.DatacenterFolders, vm *object.VirtualMachine, rp *object.ResourcePool, storagePod object.StoragePod) types.StoragePlacementSpec {
	vmr := vm.Reference()
	vmfr := f.VmFolder.Reference()
	rpr := rp.Reference()
	spr := storagePod.Reference()

	var o mo.VirtualMachine
	err := vm.Properties(context.TODO(), vmr, []string{"datastore"}, &o)
	if err != nil {
		return types.StoragePlacementSpec{}
	}
	ds := object.NewDatastore(c.Client, o.Datastore[0])
	log.Printf("[DEBUG] findDatastore: datastore: %#v\n", ds)

	devices, err := vm.Device(context.TODO())
	if err != nil {
		return types.StoragePlacementSpec{}
	}

	var key int
	for _, d := range devices.SelectByType((*types.VirtualDisk)(nil)) {
		key = d.GetVirtualDevice().Key
		log.Printf("[DEBUG] findDatastore: virtual devices: %#v\n", d.GetVirtualDevice())
	}

	sps := types.StoragePlacementSpec{
		Type: "clone",
		Vm:   &vmr,
		PodSelectionSpec: types.StorageDrsPodSelectionSpec{
			StoragePod: &spr,
		},
		CloneSpec: &types.VirtualMachineCloneSpec{
			Location: types.VirtualMachineRelocateSpec{
				Disk: []types.VirtualMachineRelocateSpecDiskLocator{
					types.VirtualMachineRelocateSpecDiskLocator{
						Datastore:       ds.Reference(),
						DiskBackingInfo: &types.VirtualDiskFlatVer2BackingInfo{},
						DiskId:          key,
					},
				},
				Pool: &rpr,
			},
			PowerOn:  false,
			Template: false,
		},
		CloneName: "dummy",
		Folder:    &vmfr,
	}
	return sps
}

// findDatastore finds Datastore object.
func findDatastore(c *govmomi.Client, sps types.StoragePlacementSpec) (*object.Datastore, error) {
	var datastore *object.Datastore
	log.Printf("[DEBUG] findDatastore: StoragePlacementSpec: %#v\n", sps)

	srm := object.NewStorageResourceManager(c.Client)
	rds, err := srm.RecommendDatastores(context.TODO(), sps)
	if err != nil {
		return nil, err
	}
	log.Printf("[DEBUG] findDatastore: recommendDatastores: %#v\n", rds)

	spa := rds.Recommendations[0].Action[0].(*types.StoragePlacementAction)
	datastore = object.NewDatastore(c.Client, spa.Destination)
	log.Printf("[DEBUG] findDatastore: datastore: %#v", datastore)

	return datastore, nil
}

// createCdroms is a helper function to attach virtual cdrom devices (and their attached disk images) to a virtual IDE controller.
func createCdroms(vm *object.VirtualMachine, cdroms []cdrom) error {
	log.Printf("[DEBUG] add cdroms: %v", cdroms)
	for _, cd := range cdroms {
		log.Printf("[DEBUG] add cdrom (datastore): %v", cd.datastore)
		log.Printf("[DEBUG] add cdrom (cd path): %v", cd.path)
		err := addCdrom(vm, cd.datastore, cd.path)
		if err != nil {
			return err
		}
	}

	return nil
}

// createVirtualMachine creates a new VirtualMachine.
func (vm *virtualMachine) createVirtualMachine(c *govmomi.Client) error {
	dc, err := getDatacenter(c, vm.datacenter)

	if err != nil {
		return err
	}
	finder := find.NewFinder(c.Client, true)
	finder = finder.SetDatacenter(dc)

	var resourcePool *object.ResourcePool
	if vm.resourcePool == "" {
		if vm.cluster == "" {
			resourcePool, err = finder.DefaultResourcePool(context.TODO())
			if err != nil {
				return err
			}
		} else {
			resourcePool, err = finder.ResourcePool(context.TODO(), "*"+vm.cluster+"/Resources")
			if err != nil {
				return err
			}
		}
	} else {
		resourcePool, err = finder.ResourcePool(context.TODO(), vm.resourcePool)
		if err != nil {
			return err
		}
	}
	log.Printf("[DEBUG] resource pool: %#v", resourcePool)

	dcFolders, err := dc.Folders(context.TODO())
	if err != nil {
		return err
	}

	log.Printf("[DEBUG] folder: %#v", vm.folder)
	folder := dcFolders.VmFolder
	if len(vm.folder) > 0 {
		si := object.NewSearchIndex(c.Client)
		folderRef, err := si.FindByInventoryPath(
			context.TODO(), fmt.Sprintf("%v/vm/%v", vm.datacenter, vm.folder))
		if err != nil {
			return fmt.Errorf("Error reading folder %s: %s", vm.folder, err)
		} else if folderRef == nil {
			return fmt.Errorf("Cannot find folder %s", vm.folder)
		} else {
			folder = folderRef.(*object.Folder)
		}
	}

	// network
	networkDevices := []types.BaseVirtualDeviceConfigSpec{}
	for _, network := range vm.networkInterfaces {
		// network device
		nd, err := buildNetworkDevice(finder, network.label, "e1000")
		if err != nil {
			return err
		}
		networkDevices = append(networkDevices, nd)
	}

	// make config spec
	configSpec := types.VirtualMachineConfigSpec{
		GuestId:           "otherLinux64Guest",
		Name:              vm.name,
		NumCPUs:           vm.vcpu,
		NumCoresPerSocket: 1,
		MemoryMB:          vm.memoryMb,
		MemoryAllocation: &types.ResourceAllocationInfo{
			Reservation: vm.memoryAllocation.reservation,
		},
		DeviceChange: networkDevices,
	}
	log.Printf("[DEBUG] virtual machine config spec: %v", configSpec)

	// make ExtraConfig
	log.Printf("[DEBUG] virtual machine Extra Config spec start")
	if len(vm.customConfigurations) > 0 {
		var ov []types.BaseOptionValue
		for k, v := range vm.customConfigurations {
			key := k
			value := v
			o := types.OptionValue{
				Key:   key,
				Value: &value,
			}
			log.Printf("[DEBUG] virtual machine Extra Config spec: %s,%s", k, v)
			ov = append(ov, &o)
		}
		configSpec.ExtraConfig = ov
		log.Printf("[DEBUG] virtual machine Extra Config spec: %v", configSpec.ExtraConfig)
	}

	var datastore *object.Datastore
	if vm.datastore == "" {
		datastore, err = finder.DefaultDatastore(context.TODO())
		if err != nil {
			return err
		}
	} else {
		datastore, err = finder.Datastore(context.TODO(), vm.datastore)
		if err != nil {
			// TODO: datastore cluster support in govmomi finder function
			d, err := getDatastoreObject(c, dcFolders, vm.datastore)
			if err != nil {
				return err
			}

			if d.Type == "StoragePod" {
				sp := object.StoragePod{
					Folder: object.NewFolder(c.Client, d),
				}
				sps := buildStoragePlacementSpecCreate(dcFolders, resourcePool, sp, configSpec)
				datastore, err = findDatastore(c, sps)
				if err != nil {
					return err
				}
			} else {
				datastore = object.NewDatastore(c.Client, d)
			}
		}
	}

	log.Printf("[DEBUG] datastore: %#v", datastore)

	var mds mo.Datastore
	if err = datastore.Properties(context.TODO(), datastore.Reference(), []string{"name"}, &mds); err != nil {
		return err
	}
	log.Printf("[DEBUG] datastore: %#v", mds.Name)
	scsi, err := object.SCSIControllerTypes().CreateSCSIController("scsi")
	if err != nil {
		log.Printf("[ERROR] %s", err)
	}

	configSpec.DeviceChange = append(configSpec.DeviceChange, &types.VirtualDeviceConfigSpec{
		Operation: types.VirtualDeviceConfigSpecOperationAdd,
		Device:    scsi,
	})

	configSpec.Files = &types.VirtualMachineFileInfo{VmPathName: fmt.Sprintf("[%s]", mds.Name)}

	task, err := folder.CreateVM(context.TODO(), configSpec, resourcePool, nil)
	if err != nil {
		log.Printf("[ERROR] %s", err)
	}

	err = task.Wait(context.TODO())
	if err != nil {
		log.Printf("[ERROR] %s", err)
	}

	newVM, err := finder.VirtualMachine(context.TODO(), vm.Path())
	if err != nil {
		return err
	}
	log.Printf("[DEBUG] new vm: %v", newVM)

	log.Printf("[DEBUG] add hard disk: %v", vm.hardDisks)
	for _, hd := range vm.hardDisks {
		log.Printf("[DEBUG] add hard disk: %v", hd.size)
		log.Printf("[DEBUG] add hard disk: %v", hd.iops)
		err = addHardDisk(newVM, hd.size, hd.iops, "thin", datastore, hd.vmdkPath)
		if err != nil {
			return err
		}
	}

	// Create the cdroms if needed.
	if err := createCdroms(newVM, vm.cdroms); err != nil {
		return err
	}

	if vm.bootableVmdk {
		newVM.PowerOn(context.TODO())
		ip, err := newVM.WaitForIP(context.TODO())
		if err != nil {
			return err
		}
		log.Printf("[DEBUG] ip address: %v", ip)
	}

	return nil
}

// deployVirtualMachine deploys a new VirtualMachine.
func (vm *virtualMachine) deployVirtualMachine(c *govmomi.Client) error {
	dc, err := getDatacenter(c, vm.datacenter)
	if err != nil {
		return err
	}
	finder := find.NewFinder(c.Client, true)
	finder = finder.SetDatacenter(dc)

	template, err := finder.VirtualMachine(context.TODO(), vm.template)
	if err != nil {
		return err
	}
	log.Printf("[DEBUG] template: %#v", template)

	var resourcePool *object.ResourcePool
	if vm.resourcePool == "" {
		if vm.cluster == "" {
			resourcePool, err = finder.DefaultResourcePool(context.TODO())
			if err != nil {
				return err
			}
		} else {
			resourcePool, err = finder.ResourcePool(context.TODO(), "*"+vm.cluster+"/Resources")
			if err != nil {
				return err
			}
		}
	} else {
		resourcePool, err = finder.ResourcePool(context.TODO(), vm.resourcePool)
		if err != nil {
			return err
		}
	}
	log.Printf("[DEBUG] resource pool: %#v", resourcePool)

	dcFolders, err := dc.Folders(context.TODO())
	if err != nil {
		return err
	}

	log.Printf("[DEBUG] folder: %#v", vm.folder)
	folder := dcFolders.VmFolder
	if len(vm.folder) > 0 {
		si := object.NewSearchIndex(c.Client)
		folderRef, err := si.FindByInventoryPath(
			context.TODO(), fmt.Sprintf("%v/vm/%v", vm.datacenter, vm.folder))
		if err != nil {
			return fmt.Errorf("Error reading folder %s: %s", vm.folder, err)
		} else if folderRef == nil {
			return fmt.Errorf("Cannot find folder %s", vm.folder)
		} else {
			folder = folderRef.(*object.Folder)
		}
	}

	var datastore *object.Datastore
	if vm.datastore == "" {
		datastore, err = finder.DefaultDatastore(context.TODO())
		if err != nil {
			return err
		}
	} else {
		datastore, err = finder.Datastore(context.TODO(), vm.datastore)
		if err != nil {
			// TODO: datastore cluster support in govmomi finder function
			d, err := getDatastoreObject(c, dcFolders, vm.datastore)
			if err != nil {
				return err
			}

			if d.Type == "StoragePod" {
				sp := object.StoragePod{
					Folder: object.NewFolder(c.Client, d),
				}
				sps := buildStoragePlacementSpecClone(c, dcFolders, template, resourcePool, sp)

				datastore, err = findDatastore(c, sps)
				if err != nil {
					return err
				}
			} else {
				datastore = object.NewDatastore(c.Client, d)
			}
		}
	}
	log.Printf("[DEBUG] datastore: %#v", datastore)

	relocateSpec, err := buildVMRelocateSpec(resourcePool, datastore, template, vm.linkedClone, vm.hardDisks[0].initType)
	if err != nil {
		return err
	}

	log.Printf("[DEBUG] relocate spec: %v", relocateSpec)

	// network
	networkDevices := []types.BaseVirtualDeviceConfigSpec{}
	networkConfigs := []types.CustomizationAdapterMapping{}
	for _, network := range vm.networkInterfaces {
		// network device
		nd, err := buildNetworkDevice(finder, network.label, "vmxnet3")
		if err != nil {
			return err
		}
		networkDevices = append(networkDevices, nd)

		var ipSetting types.CustomizationIPSettings
		if network.ipv4Address == "" {
			ipSetting.Ip = &types.CustomizationDhcpIpGenerator{}
		} else {
			if network.ipv4PrefixLength == 0 {
				return fmt.Errorf("Error: ipv4_prefix_length argument is empty.")
			}
			m := net.CIDRMask(network.ipv4PrefixLength, 32)
			sm := net.IPv4(m[0], m[1], m[2], m[3])
			subnetMask := sm.String()
			log.Printf("[DEBUG] ipv4 gateway: %v\n", network.ipv4Gateway)
			log.Printf("[DEBUG] ipv4 address: %v\n", network.ipv4Address)
			log.Printf("[DEBUG] ipv4 prefix length: %v\n", network.ipv4PrefixLength)
			log.Printf("[DEBUG] ipv4 subnet mask: %v\n", subnetMask)
			ipSetting.Gateway = []string{
				network.ipv4Gateway,
			}
			ipSetting.Ip = &types.CustomizationFixedIp{
				IpAddress: network.ipv4Address,
			}
			ipSetting.SubnetMask = subnetMask
		}

		ipv6Spec := &types.CustomizationIPSettingsIpV6AddressSpec{}
		if network.ipv6Address == "" {
			ipv6Spec.Ip = []types.BaseCustomizationIpV6Generator{
				&types.CustomizationDhcpIpV6Generator{},
			}
		} else {
			log.Printf("[DEBUG] ipv6 gateway: %v\n", network.ipv6Gateway)
			log.Printf("[DEBUG] ipv6 address: %v\n", network.ipv6Address)
			log.Printf("[DEBUG] ipv6 prefix length: %v\n", network.ipv6PrefixLength)

			ipv6Spec.Ip = []types.BaseCustomizationIpV6Generator{
				&types.CustomizationFixedIpV6{
					IpAddress:  network.ipv6Address,
					SubnetMask: network.ipv6PrefixLength,
				},
			}
			ipv6Spec.Gateway = []string{network.ipv6Gateway}
		}
		ipSetting.IpV6Spec = ipv6Spec

		// network config
		config := types.CustomizationAdapterMapping{
			Adapter: ipSetting,
		}
		networkConfigs = append(networkConfigs, config)
	}
	log.Printf("[DEBUG] network configs: %v", networkConfigs[0].Adapter)

	// make config spec
	configSpec := types.VirtualMachineConfigSpec{
		NumCPUs:           vm.vcpu,
		NumCoresPerSocket: 1,
		MemoryMB:          vm.memoryMb,
		MemoryAllocation: &types.ResourceAllocationInfo{
			Reservation: vm.memoryAllocation.reservation,
		},
	}

	log.Printf("[DEBUG] virtual machine config spec: %v", configSpec)

	log.Printf("[DEBUG] starting extra custom config spec: %v", vm.customConfigurations)

	// make ExtraConfig
	if len(vm.customConfigurations) > 0 {
		var ov []types.BaseOptionValue
		for k, v := range vm.customConfigurations {
			key := k
			value := v
			o := types.OptionValue{
				Key:   key,
				Value: &value,
			}
			ov = append(ov, &o)
		}
		configSpec.ExtraConfig = ov
		log.Printf("[DEBUG] virtual machine Extra Config spec: %v", configSpec.ExtraConfig)
	}

	var template_mo mo.VirtualMachine
	err = template.Properties(context.TODO(), template.Reference(), []string{"parent", "config.template", "config.guestId", "resourcePool", "snapshot", "guest.toolsVersionStatus2", "config.guestFullName"}, &template_mo)

	var identity_options types.BaseCustomizationIdentitySettings
	if strings.HasPrefix(template_mo.Config.GuestId, "win") {
		var timeZone int
		if vm.timeZone == "Etc/UTC" {
			vm.timeZone = "085"
		}
		timeZone, err := strconv.Atoi(vm.timeZone)
		if err != nil {
			return fmt.Errorf("Error converting TimeZone: %s", err)
		}

		guiUnattended := types.CustomizationGuiUnattended{
			AutoLogon:      false,
			AutoLogonCount: 1,
			TimeZone:       timeZone,
		}

		customIdentification := types.CustomizationIdentification{}

		userData := types.CustomizationUserData{
			ComputerName: &types.CustomizationFixedName{
				Name: strings.Split(vm.name, ".")[0],
			},
			ProductId: vm.windowsOptionalConfig.productKey,
			FullName:  "terraform",
			OrgName:   "terraform",
		}

		if vm.windowsOptionalConfig.domainUserPassword != "" && vm.windowsOptionalConfig.domainUser != "" && vm.windowsOptionalConfig.domain != "" {
			customIdentification.DomainAdminPassword = &types.CustomizationPassword{
				PlainText: true,
				Value:     vm.windowsOptionalConfig.domainUserPassword,
			}
			customIdentification.DomainAdmin = vm.windowsOptionalConfig.domainUser
			customIdentification.JoinDomain = vm.windowsOptionalConfig.domain
		}

		if vm.windowsOptionalConfig.adminPassword != "" {
			guiUnattended.Password = &types.CustomizationPassword{
				PlainText: true,
				Value:     vm.windowsOptionalConfig.adminPassword,
			}
		}

		identity_options = &types.CustomizationSysprep{
			GuiUnattended:  guiUnattended,
			Identification: customIdentification,
			UserData:       userData,
		}
	} else {
		identity_options = &types.CustomizationLinuxPrep{
			HostName: &types.CustomizationFixedName{
				Name: strings.Split(vm.name, ".")[0],
			},
			Domain:     vm.domain,
			TimeZone:   vm.timeZone,
			HwClockUTC: types.NewBool(true),
		}
	}

	// create CustomizationSpec
	customSpec := types.CustomizationSpec{
		Identity: identity_options,
		GlobalIPSettings: types.CustomizationGlobalIPSettings{
			DnsSuffixList: vm.dnsSuffixes,
			DnsServerList: vm.dnsServers,
		},
		NicSettingMap: networkConfigs,
	}
	log.Printf("[DEBUG] custom spec: %v", customSpec)

	// make vm clone spec
	cloneSpec := types.VirtualMachineCloneSpec{
		Location: relocateSpec,
		Template: false,
		Config:   &configSpec,
		PowerOn:  false,
	}
	if vm.linkedClone {
		if err != nil {
			return fmt.Errorf("Error reading base VM properties: %s", err)
		}
		if template_mo.Snapshot == nil {
			return fmt.Errorf("`linkedClone=true`, but image VM has no snapshots")
		}
		cloneSpec.Snapshot = template_mo.Snapshot.CurrentSnapshot
	}
	log.Printf("[DEBUG] clone spec: %v", cloneSpec)

	task, err := template.Clone(context.TODO(), folder, vm.name, cloneSpec)
	if err != nil {
		return err
	}

	_, err = task.WaitForResult(context.TODO(), nil)
	if err != nil {
		return err
	}

	newVM, err := finder.VirtualMachine(context.TODO(), vm.Path())
	if err != nil {
		return err
	}
	log.Printf("[DEBUG] new vm: %v", newVM)

	devices, err := newVM.Device(context.TODO())
	if err != nil {
		log.Printf("[DEBUG] Template devices can't be found")
		return err
	}

	for _, dvc := range devices {
		// Issue 3559/3560: Delete all ethernet devices to add the correct ones later
		if devices.Type(dvc) == "ethernet" {
			err := newVM.RemoveDevice(context.TODO(), dvc)
			if err != nil {
				return err
			}
		}
	}
	// Add Network devices
	for _, dvc := range networkDevices {
		err := newVM.AddDevice(
			context.TODO(), dvc.GetVirtualDeviceConfigSpec().Device)
		if err != nil {
			return err
		}
	}

	// Create the cdroms if needed.
	if err := createCdroms(newVM, vm.cdroms); err != nil {
		return err
	}

	if vm.skipCustomization {
		log.Printf("[DEBUG] VM customization skipped")
	} else {
		log.Printf("[DEBUG] VM customization starting")
		taskb, err := newVM.Customize(context.TODO(), customSpec)
		if err != nil {
			return err
		}
		_, err = taskb.WaitForResult(context.TODO(), nil)
		if err != nil {
			return err
		}
		log.Printf("[DEBUG] VM customization finished")
	}

	for i := 1; i < len(vm.hardDisks); i++ {
		err = addHardDisk(newVM, vm.hardDisks[i].size, vm.hardDisks[i].iops, vm.hardDisks[i].initType, datastore, vm.hardDisks[i].vmdkPath)
		if err != nil {
			return err
		}
	}

	log.Printf("[DEBUG] virtual machine config spec: %v", configSpec)

	newVM.PowerOn(context.TODO())

	ip, err := newVM.WaitForIP(context.TODO())
	if err != nil {
		return err
	}
	log.Printf("[DEBUG] ip address: %v", ip)

	return nil
}
