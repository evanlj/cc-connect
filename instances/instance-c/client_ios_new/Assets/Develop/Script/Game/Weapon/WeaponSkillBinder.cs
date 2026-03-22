using System.Collections.Generic;

/// <summary>
/// Centralizes weapon skill enable/disable rules when switching weapons.
/// V2 refactor note:
/// - Keep behavior identical to legacy AWeaponManager.WeaponSwitch().
/// - Some active skills should NOT be disabled during weapon switch (AKnifeSkill / ThrowGrenade / AHealSkill).
/// </summary>
public sealed class WeaponSkillBinder
{
    private readonly List<ASkillRoutine> m_abilityList;
    private readonly List<ASkillRoutine> m_passiveSkillList;

    public WeaponSkillBinder(List<ASkillRoutine> abilityList, List<ASkillRoutine> passiveSkillList)
    {
        m_abilityList = abilityList;
        m_passiveSkillList = passiveSkillList;
    }

    /// <summary>
    /// Enable skills whose ownerWeapon equals <paramref name="gun"/>.
    /// NOTE: this method only enables; it does not force-disable other skills to preserve legacy behavior.
    /// </summary>
    public void EnableSkillsForGun(AGun gun)
    {
        if (gun == null)
        {
            return;
        }

        if (m_abilityList != null)
        {
            for (int i = 0; i < m_abilityList.Count; i++)
            {
                ASkillRoutine skill = m_abilityList[i];
                if (skill == null)
                {
                    continue;
                }

                if (gun == skill.ownerWeapon)
                {
                    skill.enabled = true;
                }
            }
        }

        if (m_passiveSkillList != null)
        {
            for (int i = 0; i < m_passiveSkillList.Count; i++)
            {
                ASkillRoutine skill = m_passiveSkillList[i];
                if (skill == null)
                {
                    continue;
                }

                if (gun == skill.ownerWeapon)
                {
                    skill.enabled = true;
                }
            }
        }
    }

    /// <summary>
    /// Disable skills during weapon switching, preserving legacy "keep enabled" exceptions.
    /// </summary>
    public void DisableSkillsForSwitch()
    {
        if (m_abilityList != null)
        {
            for (int i = 0; i < m_abilityList.Count; i++)
            {
                ASkillRoutine skill = m_abilityList[i];
                if (skill == null)
                {
                    continue;
                }

                if (ShouldKeepEnabledDuringSwitch(skill))
                {
                    continue;
                }

                skill.enabled = false;
            }
        }

        if (m_passiveSkillList != null)
        {
            for (int i = 0; i < m_passiveSkillList.Count; i++)
            {
                ASkillRoutine skill = m_passiveSkillList[i];
                if (skill == null)
                {
                    continue;
                }

                skill.enabled = false;
            }
        }
    }

    private static bool ShouldKeepEnabledDuringSwitch(ASkillRoutine skill)
    {
        // Keep behavior identical to legacy:
        // - AKnifeSkill / ThrowGrenade / AHealSkill are not disabled during weapon switch.
        // - Everything else is disabled.
        if (skill is AKnifeSkill)
        {
            return true;
        }
        if (skill is ThrowGrenade)
        {
            return true;
        }
        if (skill is AHealSkill)
        {
            return true;
        }

        return false;
    }
}

