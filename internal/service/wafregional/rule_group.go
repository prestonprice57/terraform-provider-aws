// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package wafregional

import (
	"context"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/wafregional"
	awstypes "github.com/aws/aws-sdk-go-v2/service/wafregional/types"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-provider-aws/internal/conns"
	"github.com/hashicorp/terraform-provider-aws/internal/errs"
	"github.com/hashicorp/terraform-provider-aws/internal/errs/sdkdiag"
	tftags "github.com/hashicorp/terraform-provider-aws/internal/tags"
	"github.com/hashicorp/terraform-provider-aws/internal/verify"
	"github.com/hashicorp/terraform-provider-aws/names"
)

// @SDKResource("aws_wafregional_rule_group", name="Rule Group")
// @Tags(identifierAttribute="arn")
func resourceRuleGroup() *schema.Resource {
	return &schema.Resource{
		CreateWithoutTimeout: resourceRuleGroupCreate,
		ReadWithoutTimeout:   resourceRuleGroupRead,
		UpdateWithoutTimeout: resourceRuleGroupUpdate,
		DeleteWithoutTimeout: resourceRuleGroupDelete,

		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},

		Schema: map[string]*schema.Schema{
			"name": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"metric_name": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validMetricName,
			},
			"activated_rule": {
				Type:     schema.TypeSet,
				Optional: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"action": {
							Type:     schema.TypeList,
							MaxItems: 1,
							Required: true,
							Elem: &schema.Resource{
								Schema: map[string]*schema.Schema{
									"type": {
										Type:     schema.TypeString,
										Required: true,
									},
								},
							},
						},
						"priority": {
							Type:     schema.TypeInt,
							Required: true,
						},
						"rule_id": {
							Type:     schema.TypeString,
							Required: true,
						},
						"type": {
							Type:     schema.TypeString,
							Optional: true,
							Default:  awstypes.WafRuleTypeRegular,
						},
					},
				},
			},
			names.AttrTags:    tftags.TagsSchema(),
			names.AttrTagsAll: tftags.TagsSchemaComputed(),
			"arn": {
				Type:     schema.TypeString,
				Computed: true,
			},
		},

		CustomizeDiff: verify.SetTagsDiff,
	}
}

func resourceRuleGroupCreate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	conn := meta.(*conns.AWSClient).WAFRegionalClient(ctx)
	region := meta.(*conns.AWSClient).Region

	name := d.Get("name").(string)
	outputRaw, err := NewRetryer(conn, region).RetryWithToken(ctx, func(token *string) (interface{}, error) {
		input := &wafregional.CreateRuleGroupInput{
			ChangeToken: token,
			MetricName:  aws.String(d.Get("metric_name").(string)),
			Name:        aws.String(name),
			Tags:        getTagsIn(ctx),
		}

		return conn.CreateRuleGroup(ctx, input)
	})

	if err != nil {
		return sdkdiag.AppendErrorf(diags, "creating WAF Regional Rule Group (%s): %s", name, err)
	}

	d.SetId(aws.ToString(outputRaw.(*wafregional.CreateRuleGroupOutput).RuleGroup.RuleGroupId))

	activatedRule := d.Get("activated_rule").(*schema.Set).List()
	if len(activatedRule) > 0 {
		noActivatedRules := []interface{}{}

		if err := updateRuleGroupResourceWR(ctx, conn, region, d.Id(), noActivatedRules, activatedRule); err != nil {
			return sdkdiag.AppendErrorf(diags, "updating WAF Regional Rule Group (%s): %s", d.Id(), err)
		}
	}

	return append(diags, resourceRuleGroupRead(ctx, d, meta)...)
}

func resourceRuleGroupRead(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	conn := meta.(*conns.AWSClient).WAFRegionalClient(ctx)

	params := &wafregional.GetRuleGroupInput{
		RuleGroupId: aws.String(d.Id()),
	}

	resp, err := conn.GetRuleGroup(ctx, params)
	if err != nil {
		if !d.IsNewResource() && errs.IsA[*awstypes.WAFNonexistentItemException](err) {
			log.Printf("[WARN] WAF Regional Rule Group (%s) not found, removing from state", d.Id())
			d.SetId("")
			return diags
		}

		return sdkdiag.AppendErrorf(diags, "reading WAF Regional Rule Group (%s): %s", d.Id(), err)
	}

	rResp, err := conn.ListActivatedRulesInRuleGroup(ctx, &wafregional.ListActivatedRulesInRuleGroupInput{
		RuleGroupId: aws.String(d.Id()),
	})
	if err != nil {
		return sdkdiag.AppendErrorf(diags, "listing activated rules in WAF Regional Rule Group (%s): %s", d.Id(), err)
	}

	arn := arn.ARN{
		AccountID: meta.(*conns.AWSClient).AccountID,
		Partition: meta.(*conns.AWSClient).Partition,
		Region:    meta.(*conns.AWSClient).Region,
		Resource:  fmt.Sprintf("rulegroup/%s", d.Id()),
		Service:   "waf-regional",
	}.String()
	d.Set("arn", arn)
	d.Set("activated_rule", FlattenActivatedRules(rResp.ActivatedRules))
	d.Set("name", resp.RuleGroup.Name)
	d.Set("metric_name", resp.RuleGroup.MetricName)

	return diags
}

func resourceRuleGroupUpdate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	conn := meta.(*conns.AWSClient).WAFRegionalClient(ctx)
	region := meta.(*conns.AWSClient).Region

	if d.HasChange("activated_rule") {
		o, n := d.GetChange("activated_rule")
		oldRules, newRules := o.(*schema.Set).List(), n.(*schema.Set).List()

		if err := updateRuleGroupResourceWR(ctx, conn, region, d.Id(), oldRules, newRules); err != nil {
			return sdkdiag.AppendErrorf(diags, "updating WAF Regional Rule Group (%s): %s", d.Id(), err)
		}
	}

	return append(diags, resourceRuleGroupRead(ctx, d, meta)...)
}

func resourceRuleGroupDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	conn := meta.(*conns.AWSClient).WAFRegionalClient(ctx)
	region := meta.(*conns.AWSClient).Region

	oldRules := d.Get("activated_rule").(*schema.Set).List()
	err := DeleteRuleGroup(ctx, d.Id(), oldRules, conn, region)

	if err != nil {
		return sdkdiag.AppendErrorf(diags, "deleting WAF Regional Rule Group (%s): %s", d.Id(), err)
	}

	return diags
}

func DeleteRuleGroup(ctx context.Context, ruleGroupID string, oldRules []interface{}, conn *wafregional.Client, region string) error {
	if len(oldRules) > 0 {
		noRules := []interface{}{}

		err := updateRuleGroupResourceWR(ctx, conn, region, ruleGroupID, oldRules, noRules)

		if errs.IsA[*awstypes.WAFNonexistentContainerException](err) || errs.IsA[*awstypes.WAFNonexistentItemException](err) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("updating WAF Regional Rule Group (%s): %s", ruleGroupID, err)
		}
	}

	log.Printf("[INFO] Deleting WAF Regional Rule Group: %s", ruleGroupID)
	_, err := NewRetryer(conn, region).RetryWithToken(ctx, func(token *string) (interface{}, error) {
		input := &wafregional.DeleteRuleGroupInput{
			ChangeToken: token,
			RuleGroupId: aws.String(ruleGroupID),
		}

		return conn.DeleteRuleGroup(ctx, input)
	})

	if errs.IsA[*awstypes.WAFNonexistentItemException](err) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("deleting WAF Regional Rule Group (%s): %s", ruleGroupID, err)
	}

	return nil
}

func updateRuleGroupResourceWR(ctx context.Context, conn *wafregional.Client, region, ruleGroupID string, oldRules, newRules []interface{}) error {
	_, err := NewRetryer(conn, region).RetryWithToken(ctx, func(token *string) (interface{}, error) {
		input := &wafregional.UpdateRuleGroupInput{
			ChangeToken: token,
			RuleGroupId: aws.String(ruleGroupID),
			Updates:     DiffRuleGroupActivatedRules(oldRules, newRules),
		}

		return conn.UpdateRuleGroup(ctx, input)
	})

	return err
}
